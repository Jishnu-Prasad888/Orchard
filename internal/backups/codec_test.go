package backups

import (
	"bytes"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	mrand "math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Jishnu-Prasad888/Cairn/internal/crypto"
)

// testKey is the fixed 32-byte key used by codec tests.
var testKey = []byte("0123456789abcdef0123456789abcdef")

// roundTrip writes plaintext through a sink into a bytes.Buffer-backed file,
// then reads it back through openPayload, asserting integrity and content.
func roundTrip(t *testing.T, payload []byte, compress, encrypted bool) {
	t.Helper()

	sink, err := newPayloadSink(compress, encrypted, testKey)
	if err != nil {
		t.Fatal(err)
	}
	defer sink.abort()

	dstFile := filepath.Join(t.TempDir(), "out")
	dst, err := os.Create(dstFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sink.Write(payload); err != nil {
		t.Fatal(err)
	}
	h, err := sink.Materialize(dst)
	if err != nil {
		t.Fatal(err)
	}
	if err := dst.Close(); err != nil {
		t.Fatal(err)
	}

	var key []byte
	if encrypted {
		key = testKey
	}
	raw, err := os.ReadFile(dstFile)
	if err != nil {
		t.Fatal(err)
	}
	if encrypted {
		if !h.AEADSealed {
			t.Fatal("encrypted payload must be marked AEAD-sealed")
		}
		if len(raw) > headerSize && !crypto.IsSealed(raw[headerSize:]) {
			t.Fatal("encrypted payload body must start with the sealed-blob magic")
		}
	} else if h.AEADSealed {
		t.Fatal("plaintext payload must not be marked AEAD-sealed")
	}

	src, err := os.Open(dstFile)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = src.Close() }()

	pr, err := openPayload(src, key)
	if err != nil {
		t.Fatalf("openPayload: %v", err)
	}
	if h.Encrypted != pr.Header().Encrypted || h.Compressed != pr.Header().Compressed {
		t.Fatalf("header round-trip mismatch: wrote %+v read %+v", h, pr.Header())
	}
	body, err := io.ReadAll(pr)
	if err != nil {
		t.Fatal(err)
	}
	if err := pr.Err(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, payload) {
		t.Fatalf("payload mismatch: got %q want %q", body, payload)
	}
}

func TestPayloadRoundTrip(t *testing.T) {
	payload := []byte("hello cairn — backup me, please")
	cases := []struct {
		name      string
		payload   []byte
		compress  bool
		encrypted bool
	}{
		{"raw", payload, false, false},
		{"compressed", payload, true, false},
		{"encrypted", payload, false, true},
		{"compressed+encrypted", payload, true, true},
		{"empty", nil, false, false},
		{"empty encrypted", nil, false, true},
		{"empty encrypted compressed", nil, true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			roundTrip(t, c.payload, c.compress, c.encrypted)
		})
	}
}

// pseudoRandom returns n deterministic, high-entropy bytes so gzip cannot
// meaningfully shrink them and the chunk math stays predictable.
func pseudoRandom(rng *mrand.Rand, n int) []byte {
	b := make([]byte, n)
	if _, err := rng.Read(b); err != nil {
		panic(err)
	}
	return b
}

// TestAEADChunkBoundaries fixes the chunk-boundary math of the sealed payload
// format: StoredSize must reflect the per-chunk sealed sizes, the body must be
// sealed, and multiple chunks must read back byte-identically.
func TestAEADChunkBoundaries(t *testing.T) {
	rng := mrand.New(mrand.NewSource(42))
	sizes := []int{
		1,
		aeadChunkSize - 1,
		aeadChunkSize,
		aeadChunkSize + 1,
		2*aeadChunkSize - 1,
		2 * aeadChunkSize,
		2*aeadChunkSize + 37,
		3*aeadChunkSize + 123,
	}
	for _, size := range sizes {
		name := fmt.Sprintf("plaintext_%d", size)
		t.Run(name, func(t *testing.T) {
			payload := pseudoRandom(rng, size)
			sink, err := newPayloadSink(false, true, testKey)
			if err != nil {
				t.Fatal(err)
			}
			defer sink.abort()
			dstFile := filepath.Join(t.TempDir(), "out")
			dst, err := os.Create(dstFile)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := sink.Write(payload); err != nil {
				t.Fatal(err)
			}
			h, err := sink.Materialize(dst)
			if err != nil {
				t.Fatal(err)
			}
			_ = dst.Close()

			if !h.AEADSealed || !h.Encrypted {
				t.Fatalf("header not AEAD: %+v", h)
			}
			nChunks := (size + aeadChunkSize - 1) / aeadChunkSize
			wantStored := size + nChunks*(crypto.SealHeaderLen()+crypto.SealTagLen())
			if int(h.StoredSize) != wantStored {
				t.Fatalf("StoredSize = %d, want sealed size %d (plaintext %d, %d chunks)",
					h.StoredSize, wantStored, size, nChunks)
			}

			f, _ := os.Open(dstFile)
			defer func() { _ = f.Close() }()
			pr, err := openPayload(f, testKey)
			if err != nil {
				t.Fatalf("openPayload: %v", err)
			}
			got, err := io.ReadAll(pr)
			if err != nil {
				t.Fatal(err)
			}
			if err := pr.Err(); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, payload) {
				t.Fatal("chunked payload did not round-trip")
			}
		})
	}
}

// TestAEADRejectsWrongKey asserts that opening a sealed payload with a
// different key fails authenticated (ErrInvalidPassphrase), never yielding
// garbage.
func TestAEADRejectsWrongKey(t *testing.T) {
	payload := pseudoRandom(mrand.New(mrand.NewSource(1)), aeadChunkSize+10)
	sink, err := newPayloadSink(false, true, testKey)
	if err != nil {
		t.Fatal(err)
	}
	defer sink.abort()
	fn := filepath.Join(t.TempDir(), "p")
	dst, err := os.Create(fn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sink.Write(payload); err != nil {
		t.Fatal(err)
	}
	if _, err := sink.Materialize(dst); err != nil {
		t.Fatal(err)
	}
	_ = dst.Close()

	wrongKey := []byte("fedcba9876543210fedcba9876543210")
	f, _ := os.Open(fn)
	defer func() { _ = f.Close() }()
	pr, err := openPayload(f, wrongKey)
	if err != nil {
		if !errors.Is(err, crypto.ErrInvalidPassphrase) {
			t.Fatalf("openPayload: %v, want ErrInvalidPassphrase", err)
		}
		return
	}
	if _, err := io.Copy(io.Discard, pr); !errors.Is(err, crypto.ErrInvalidPassphrase) {
		t.Fatalf("read with wrong key: %v, want ErrInvalidPassphrase", err)
	}
}

// TestAEADDetectsTampering flips a byte near the tail of a stored payload and
// requires the tampering to be rejected (at open or on read) by GCM
// authentication.
func TestAEADDetectsTampering(t *testing.T) {
	sink, err := newPayloadSink(true, true, testKey)
	if err != nil {
		t.Fatal(err)
	}
	defer sink.abort()
	fn := filepath.Join(t.TempDir(), "p")
	dst, err := os.Create(fn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sink.Write(pseudoRandom(mrand.New(mrand.NewSource(7)), 2*aeadChunkSize+500)); err != nil {
		t.Fatal(err)
	}
	if _, err := sink.Materialize(dst); err != nil {
		t.Fatal(err)
	}
	_ = dst.Close()

	b, err := os.ReadFile(fn)
	if err != nil {
		t.Fatal(err)
	}
	b[len(b)-6] ^= 0xff
	if err := os.WriteFile(fn, b, 0o600); err != nil {
		t.Fatal(err)
	}

	f, _ := os.Open(fn)
	defer func() { _ = f.Close() }()
	pr, err := openPayload(f, testKey)
	if err != nil {
		return // caught at open
	}
	if _, err := io.Copy(io.Discard, pr); err == nil {
		if err := pr.Err(); err == nil {
			t.Fatal("tampered AEAD payload was not rejected")
		}
	}
}

// writeLegacyCTRPayload replicates the pre-Phase-14 (Phase 10) encrypted
// payload codec exactly: AES-256-CTR ciphertext with a 16-byte IV after the
// header, SHA-256 over the plaintext, and no AEAD flag. It is used to build a
// legacy-format fixture so the retained legacy reader stays pinned.
func writeLegacyCTRPayload(dst io.Writer, key []byte, compress bool, plain []byte) error {
	body := plain
	if compress {
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		gz.ModTime = time.Time{}
		if _, err := gz.Write(plain); err != nil {
			return err
		}
		if err := gz.Close(); err != nil {
			return err
		}
		body = buf.Bytes()
	}
	sum := sha256.Sum256(plain)
	h := fileHeader{
		Compressed:    compress,
		Encrypted:     true,
		AEADSealed:    false,
		SHA256Hex:     hex.EncodeToString(sum[:]),
		PlaintextSize: uint64(len(plain)),
		StoredSize:    uint64(len(body)),
	}
	if _, err := dst.Write(h.encode()); err != nil {
		return err
	}
	nonce := make([]byte, nonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return err
	}
	if _, err := dst.Write(nonce); err != nil {
		return err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	sealed := make([]byte, len(body))
	cipher.NewCTR(block, nonce).XORKeyStream(sealed, body)
	_, err = dst.Write(sealed)
	return err
}

// TestLegacyCTRPayloadStillReads pins the backward-compatibility guarantee:
// a Phase-10 CTR payload (no AEAD magic, no AEAD flag) must still open, verify,
// and restore byte-identically through the retained legacy branch.
func TestLegacyCTRPayloadStillReads(t *testing.T) {
	plain := pseudoRandom(mrand.New(mrand.NewSource(3)), 100000)
	for _, compress := range []bool{false, true} {
		name := "raw"
		if compress {
			name = "compressed"
		}
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := writeLegacyCTRPayload(&buf, testKey, compress, plain); err != nil {
				t.Fatal(err)
			}
			raw := buf.Bytes()
			if crypto.IsSealed(raw[headerSize:]) {
				t.Fatal("legacy fixture must not be mistaken for an AEAD payload")
			}
			pr, err := openPayload(bytes.NewReader(raw), testKey)
			if err != nil {
				t.Fatalf("open legacy payload: %v", err)
			}
			if pr.Header().AEADSealed {
				t.Fatal("legacy payload must not report AEADSealed")
			}
			got, err := io.ReadAll(pr)
			if err != nil {
				t.Fatalf("read legacy payload: %v", err)
			}
			if err := pr.Err(); err != nil {
				t.Fatalf("legacy payload checksum: %v", err)
			}
			if !bytes.Equal(got, plain) {
				t.Fatal("legacy payload did not restore byte-identically")
			}
		})
	}
}

func TestPayloadDetectsCorruption(t *testing.T) {
	testKey := []byte("0123456789abcdef0123456789abcdef")
	sink, err := newPayloadSink(true, true, testKey)
	if err != nil {
		t.Fatal(err)
	}
	defer sink.abort()
	fn := filepath.Join(t.TempDir(), "p")
	dst, err := os.Create(fn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sink.Write([]byte(strings.Repeat("a", 5000))); err != nil {
		t.Fatal(err)
	}
	if _, err := sink.Materialize(dst); err != nil {
		t.Fatal(err)
	}
	_ = dst.Close()

	b, err := os.ReadFile(fn)
	if err != nil {
		t.Fatal(err)
	}
	// Flip a bit near the tail, inside the payload (past header+gzip header).
	b[len(b)-6] ^= 0xff
	if err := os.WriteFile(fn, b, 0o600); err != nil {
		t.Fatal(err)
	}

	f, _ := os.Open(fn)
	defer func() { _ = f.Close() }()
	// AEAD authentication can now fail fast as soon as the payload is opened
	// (gzip.NewReader triggers the first chunk's authenticated read); otherwise
	// it must be caught while streaming.
	pr, err := openPayload(f, testKey)
	if err != nil {
		return // corruption detected at open
	}
	if _, err := io.Copy(io.Discard, pr); err == nil {
		if err := pr.Err(); err == nil {
			t.Fatal("expected corruption to be detected (read error or checksum error)")
		}
	}
}

func TestPayloadRejectsTruncation(t *testing.T) {
	sink, err := newPayloadSink(false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sink.abort()
	fn := filepath.Join(t.TempDir(), "p")
	dst, err := os.Create(fn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sink.Write([]byte(strings.Repeat("b", 2000))); err != nil {
		t.Fatal(err)
	}
	if _, err := sink.Materialize(dst); err != nil {
		t.Fatal(err)
	}
	_ = dst.Close()

	// Truncate the payload so the declared plaintext size can never be met.
	// Keep the 57-byte header intact so openPayload succeeds and the short
	// payload is what must be caught.
	if err := os.Truncate(fn, 67); err != nil {
		t.Fatal(err)
	}

	f, _ := os.Open(fn)
	defer func() { _ = f.Close() }()
	pr, err := openPayload(f, nil)
	if err != nil {
		t.Fatalf("openPayload: %v", err)
	}
	if _, err := io.Copy(io.Discard, pr); err == nil {
		if pr.Err() == nil {
			t.Fatal("expected truncation to be detected")
		}
	}
}

func TestKeyDerivationDeterministic(t *testing.T) {
	salt := []byte("fixed-salt-16b")
	a := deriveKey("s3cret", salt)
	b := deriveKey("s3cret", salt)
	c := deriveKey("other", salt)
	if string(a) != string(b) {
		t.Fatal("same passphrase+salt must derive same key")
	}
	if string(a) == string(c) {
		t.Fatal("different passphrase must derive different key")
	}
	if len(a) != 32 {
		t.Fatalf("key length = %d, want 32", len(a))
	}
}
