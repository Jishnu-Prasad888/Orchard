// Package backups implements Phase 10: incremental, compressed, optionally
// encrypted backups of every registered library (media files and .cairn
// metadata) and of the server database, with retention, verification, and
// restore. See docs/backups.md and ADR-0008.
package backups

import (
	"bufio"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"time"

	"github.com/Jishnu-Prasad888/Cairn/internal/crypto"
	"golang.org/x/crypto/argon2"
)

// formatMagic identifies a Cairn backup payload.
var formatMagic = [8]byte{'C', 'A', 'I', 'R', 'N', 'B', 'K', 0x01}

// headerSize is the fixed binary header preceding every stored payload:
//
//	[8] magic | [1] flags | [32] sha256 | [8] plaintext size | [8] stored size
//
// For legacy CTR payloads the header is immediately followed by the 16-byte IV;
// AEAD-sealed payloads carry no separate IV (each sealed chunk embeds its own).
const headerSize = 8 + 1 + 32 + 8 + 8

// nonceSize is the AES block-size IV carried after the header by legacy CTR
// payloads only (pre-Phase-14 encrypted backups).
const nonceSize = aes.BlockSize

// aeadChunkSize is the plaintext size of each independently sealed chunk when
// a payload is encrypted with the shared AEAD kernel (Phase 14+). It bounds
// memory to a chunk at a time regardless of the payload's total size.
const aeadChunkSize = 64 * 1024

const (
	flagCompressed = 1 << 0
	flagEncrypted  = 1 << 1
	flagAEADSealed = 1 << 2
)

// fileHeader describes one stored payload.
type fileHeader struct {
	Compressed    bool
	Encrypted     bool
	AEADSealed    bool
	SHA256Hex     string
	PlaintextSize uint64
	StoredSize    uint64
}

func (h fileHeader) encode() []byte {
	buf := make([]byte, 0, headerSize)
	buf = append(buf, formatMagic[:]...)
	var flags byte
	if h.Compressed {
		flags |= flagCompressed
	}
	if h.Encrypted {
		flags |= flagEncrypted
	}
	if h.AEADSealed {
		flags |= flagAEADSealed
	}
	buf = append(buf, flags)
	sum, err := hex.DecodeString(h.SHA256Hex)
	if err != nil || len(sum) != sha256.Size {
		sum = make([]byte, sha256.Size)
	}
	buf = append(buf, sum...)
	buf = binary.BigEndian.AppendUint64(buf, h.PlaintextSize)
	buf = binary.BigEndian.AppendUint64(buf, h.StoredSize)
	return buf
}

// decodeHeader parses the leading header bytes and checks the magic.
func decodeHeader(b []byte) (fileHeader, error) {
	if len(b) < headerSize {
		return fileHeader{}, errors.New("backup payload header truncated")
	}
	if string(b[:8]) != string(formatMagic[:]) {
		return fileHeader{}, errors.New("not a Cairn backup payload (bad magic)")
	}
	var h fileHeader
	flags := b[8]
	h.Compressed = flags&flagCompressed != 0
	h.Encrypted = flags&flagEncrypted != 0
	h.AEADSealed = flags&flagAEADSealed != 0
	h.SHA256Hex = hex.EncodeToString(b[9:41])
	h.PlaintextSize = binary.BigEndian.Uint64(b[41:49])
	h.StoredSize = binary.BigEndian.Uint64(b[49:57])
	return h, nil
}

// deriveKey derives a 32-byte AES-256 key from a passphrase and salt via
// argon2id, matching the password-hashing conventions used elsewhere in Cairn.
// The derived key is handed to crypto.NewKeysFromKey by the sealing/opening
// paths so backup payloads use the shared AEAD kernel's blob format.
func deriveKey(passphrase string, salt []byte) []byte {
	return argon2.IDKey([]byte(passphrase), salt, 1, 64*1024, 4, 32)
}

// payloadSink writes one stored payload. Plaintext is hashed and optionally
// gzip'd into a temp file as it arrives; Materialize then streams the temp
// content into the final destination with the fixed header in front. When
// encryption is enabled the temp content is sealed in aeadChunkSize chunks with
// the shared AEAD kernel (crypto.NewKeysFromKey derived from the caller's
// argon2id-derived key), so memory stays bounded by one chunk and the whole
// body is a sequence of authenticated sealed blobs. The header's StoredSize
// reflects the sealed size; the legacy CTR path that preceded Phase 14 is
// retained read-only for old backups.
type payloadSink struct {
	tmp       *os.File
	hasher    hash.Hash
	gz        *gzip.Writer
	compress  bool
	encrypted bool
	key       []byte
	nbytes    int64
}

// newPayloadSink prepares a sink. compress applies deterministic gzip (no mtime)
// so byte-identical unchanged files are hard-linkable across backups; encrypted
// requires a 32-byte key.
func newPayloadSink(compress, encrypted bool, key []byte) (*payloadSink, error) {
	tmp, err := os.CreateTemp("", "cairn-payload-*")
	if err != nil {
		return nil, fmt.Errorf("create payload temp file: %w", err)
	}
	s := &payloadSink{
		tmp:       tmp,
		hasher:    sha256.New(),
		compress:  compress,
		encrypted: encrypted,
		key:       key,
	}
	if compress {
		s.gz = gzip.NewWriter(tmp)
		s.gz.ModTime = time.Time{}
	}
	return s, nil
}

// Write accepts plaintext.
func (s *payloadSink) Write(p []byte) (int, error) {
	if _, err := s.hasher.Write(p); err != nil {
		return 0, err
	}
	s.nbytes += int64(len(p))
	if s.gz != nil {
		return s.gz.Write(p)
	}
	return s.tmp.Write(p)
}

// Materialize writes the finalized payload into dst and returns the header for
// the manifest. It must be called exactly once; the temp file is removed.
func (s *payloadSink) Materialize(dst io.Writer) (fileHeader, error) {
	if s.gz != nil {
		if err := s.gz.Close(); err != nil {
			return fileHeader{}, fmt.Errorf("flush gzip: %w", err)
		}
	}
	stat, err := s.tmp.Stat()
	if err != nil {
		return fileHeader{}, err
	}
	storedSize := stat.Size()

	h := fileHeader{
		Compressed:    s.compress,
		Encrypted:     s.encrypted,
		AEADSealed:    s.encrypted,
		SHA256Hex:     hex.EncodeToString(s.hasher.Sum(nil)),
		PlaintextSize: uint64(s.nbytes),
	}
	if s.encrypted {
		// The sealed body is larger than the (possibly compressed) plaintext:
		// every chunk gains the sealed-blob header (magic+version+nonce) and a
		// GCM tag. The header must record the sealed size so readers know the
		// exact body length. The math below mirrors the chunking performed by
		// the sealing loop.
		nChunks := (stat.Size() + int64(aeadChunkSize) - 1) / int64(aeadChunkSize)
		storedSize = stat.Size() + nChunks*int64(crypto.SealHeaderLen()+crypto.SealTagLen())
	}
	h.StoredSize = uint64(storedSize)

	if _, err := dst.Write(h.encode()); err != nil {
		return fileHeader{}, err
	}

	if _, err := s.tmp.Seek(0, io.SeekStart); err != nil {
		return fileHeader{}, err
	}

	if !s.encrypted {
		if _, err := io.Copy(dst, bufio.NewReader(s.tmp)); err != nil {
			return fileHeader{}, err
		}
		if err := os.Remove(s.tmp.Name()); err != nil {
			return fileHeader{}, fmt.Errorf("remove payload temp: %w", err)
		}
		return h, nil
	}

	keys := crypto.NewKeysFromKey(s.key)
	var buf [aeadChunkSize]byte
	for {
		n, rerr := s.tmp.Read(buf[:])
		if n > 0 {
			sealed := keys.Seal(buf[:n])
			if _, werr := dst.Write(sealed); werr != nil {
				return fileHeader{}, werr
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return fileHeader{}, rerr
		}
	}
	if err := os.Remove(s.tmp.Name()); err != nil {
		return fileHeader{}, fmt.Errorf("remove payload temp: %w", err)
	}
	return h, nil
}

// idempotentCleanup lets callers remove the temp file on error paths.
func (s *payloadSink) abort() {
	_ = os.Remove(s.tmp.Name())
}

// payloadReader streams a stored payload back into plaintext, verifying the
// SHA-256 and sizes as it goes. StoredSize serves as an integrity check against
// truncation; the hash catches corruption. For AEAD-sealed payloads the GCM
// authentication additionally catches tampering and wrong passphrases
// (crypto.ErrInvalidPassphrase / crypto.ErrCorrupt).
type payloadReader struct {
	body io.Reader
	gz   *gzip.Reader
	h    fileHeader
	sum  hash.Hash
	read int64

	done bool
	err  error
}

// openPayload validates the header and prepares the reader. key may be nil
// unless the payload is encrypted.
func openPayload(r io.Reader, key []byte) (*payloadReader, error) {
	head := make([]byte, headerSize)
	if _, err := io.ReadFull(r, head); err != nil {
		return nil, fmt.Errorf("read payload header: %w", err)
	}
	h, err := decodeHeader(head)
	if err != nil {
		return nil, err
	}
	br := bufio.NewReader(r)
	body := io.Reader(br)
	if h.Encrypted {
		if len(key) == 0 || len(key) != 32 {
			return nil, errors.New("encrypted backup payload but no key available")
		}
		// The body of a Phase-14+ encrypted payload is a sequence of sealed
		// blobs and starts with the AEAD kernel's magic; a pre-Phase-14 CTR
		// payload starts with its random IV. The AEAD flag is authoritative
		// (the header and body are written together), and the magic probe
		// additionally lets a payload be read correctly even if its flag bit
		// were ever set inconsistently.
		peek, _ := br.Peek(9)
		if h.AEADSealed || crypto.IsSealed(peek) {
			body = newAEADReader(br, key, int64(h.StoredSize))
		} else {
			nonce := make([]byte, nonceSize)
			if _, err := io.ReadFull(br, nonce); err != nil {
				return nil, fmt.Errorf("read nonce: %w", err)
			}
			block, err := aes.NewCipher(key)
			if err != nil {
				return nil, err
			}
			body = cipher.StreamReader{S: cipher.NewCTR(block, nonce), R: br}
		}
	}

	pr := &payloadReader{
		h:    h,
		sum:  sha256.New(),
		body: body,
	}
	if h.Compressed {
		pr.gz, err = gzip.NewReader(body)
		if err != nil {
			return nil, fmt.Errorf("open gzip payload: %w", err)
		}
	}
	return pr, nil
}

// aeadReader streams a chunked AEAD-sealed payload back into plaintext with
// bounded memory. Every chunk is an independent sealed blob (magic + version +
// nonce + GCM ciphertext + tag) produced by crypto.Keys.Seal; all chunks hold
// aeadChunkSize bytes of plaintext except the last, which holds the remainder.
// Chunk boundaries are therefore a deterministic function of the header's
// StoredSize, no framing is stored, and a tampered, truncated, or wrong-key
// payload fails authentication on the first offending chunk.
type aeadReader struct {
	r         *bufio.Reader
	keys      *crypto.Keys
	remaining int64  // sealed bytes not yet consumed
	buf       []byte // leftover decrypted bytes beyond the most recent Read
	err       error
}

func newAEADReader(r *bufio.Reader, key []byte, storedSize int64) *aeadReader {
	return &aeadReader{
		r:         r,
		keys:      crypto.NewKeysFromKey(key),
		remaining: storedSize,
	}
}

// Read decrypts the next bytes into p.
func (a *aeadReader) Read(p []byte) (int, error) {
	if a.err != nil {
		return 0, a.err
	}
	if len(a.buf) > 0 {
		n := copy(p, a.buf)
		a.buf = a.buf[n:]
		if len(a.buf) == 0 {
			a.buf = nil
		}
		return n, nil
	}
	if a.remaining == 0 {
		a.err = io.EOF
		return 0, io.EOF
	}

	headerLen := crypto.SealHeaderLen()
	if a.remaining < int64(headerLen) {
		a.err = fmt.Errorf("%w: truncated sealed payload", crypto.ErrCorrupt)
		return 0, a.err
	}
	head := make([]byte, headerLen)
	if _, err := io.ReadFull(a.r, head); err != nil {
		a.err = fmt.Errorf("%w: reading sealed header: %v", crypto.ErrCorrupt, err)
		return 0, a.err
	}
	if !crypto.IsSealed(head) {
		a.err = fmt.Errorf("%w: sealed payload missing AEAD magic", crypto.ErrCorrupt)
		return 0, a.err
	}
	a.remaining -= int64(headerLen)

	tag := crypto.SealTagLen()
	var ctLen int
	if a.remaining >= int64(aeadChunkSize)+int64(tag) {
		ctLen = aeadChunkSize
	} else {
		if a.remaining < int64(tag) {
			a.err = fmt.Errorf("%w: truncated sealed chunk", crypto.ErrCorrupt)
			return 0, a.err
		}
		ctLen = int(a.remaining - int64(tag))
	}
	ct := make([]byte, ctLen+tag)
	if _, err := io.ReadFull(a.r, ct); err != nil {
		a.err = fmt.Errorf("%w: reading sealed chunk: %v", crypto.ErrCorrupt, err)
		return 0, a.err
	}
	a.remaining -= int64(len(ct))

	blob := make([]byte, headerLen+len(ct))
	copy(blob, head)
	copy(blob[headerLen:], ct)
	plain, err := a.keys.Open(blob)
	if err != nil {
		a.err = err
		return 0, a.err
	}
	n := copy(p, plain)
	if n < len(plain) {
		a.buf = append(a.buf[:0], plain[n:]...)
	}
	return n, nil
}

// Read returns decrypted, decompressed plaintext.
func (pr *payloadReader) Read(p []byte) (int, error) {
	if pr.done {
		return 0, io.EOF
	}
	var n int
	var err error
	if pr.gz != nil {
		n, err = pr.gz.Read(p)
	} else {
		n, err = pr.body.Read(p)
	}
	if n > 0 {
		_, _ = pr.sum.Write(p[:n])
		pr.read += int64(n)
	}
	if err == io.EOF {
		pr.finish()
	} else if err != nil {
		pr.err = err
	}
	return n, err
}

// finish validates completeness and integrity.
func (pr *payloadReader) finish() {
	defer func() { pr.done = true }()
	if pr.read != int64(pr.h.PlaintextSize) {
		pr.err = fmt.Errorf("payload size mismatch: got %d, header %d", pr.read, pr.h.PlaintextSize)
		return
	}
	if got := hex.EncodeToString(pr.sum.Sum(nil)); got != pr.h.SHA256Hex {
		pr.err = fmt.Errorf("payload checksum mismatch: got %s, header %s", got, pr.h.SHA256Hex)
	}
}

// Err returns a verification error when the payload was corrupt or truncated.
func (pr *payloadReader) Err() error {
	if pr.err != nil {
		return pr.err
	}
	return nil
}

// Header exposes the stored file header for reporting.
func (pr *payloadReader) Header() fileHeader { return pr.h }
