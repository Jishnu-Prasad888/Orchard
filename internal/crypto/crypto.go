// Package crypto provides the shared at-rest encryption kernel for Cairn.
//
// It implements the Phase 13 design (docs/encryption.md, ADR-0011): a single
// optional passphrase-derived AES-256-GCM key that seals and opens Cairn-owned
// on-disk data (the .cairn identity and generated thumbnails). It is shared by
// library, metadata, and httpapi so the passphrase, the derivation, and the
// on-disk blob format are defined in exactly one place.
//
// The key is derived from the passphrase with argon2id using a fixed protocol
// salt, so nothing is persisted (no key file, no salt) and the same passphrase
// always reproduces the same key across restarts. Encryption is optional: with
// an empty passphrase the Keys are disabled and Seal/Open are transparent
// passthroughs, so unencrypted installs are byte-identical to before and
// legacy plaintext artifacts remain readable after enabling encryption.
package crypto

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"

	"golang.org/x/crypto/argon2"
)

const (
	// protocolSalt is the fixed, version-pinned salt used for key derivation.
	// It is intentionally not secret and is never written to disk; the secret
	// input is entirely the passphrase. Keeping it constant means any Cairn
	// process that knows the passphrase can derive the same key, with nothing
	// stored alongside the data.
	protocolSalt = "cairn-at-rest-v1"

	// magic and formatVersion form the self-describing sealed blob header.
	// formatMagic is a fixed 8-byte magic ("CAIRNATR") and formatVersion is the
	// current AEAD scheme. blobHeaderLen = len(magic) + 1 version byte.
	magic         = "CAIRNATR"
	formatVersion = byte(1)
	blobHeaderLen = len(magic) + 1
	nonceLen      = 12 // AES-GCM standard nonce size
	keyLen        = 32 // AES-256
	saltLen       = 16 // argon2id salt (2 x hashLen)
	// keyTime, keyMem, keyThreads mirror the passphrase derivation parameters
	// used by backups (docs/adr/0012-backup-aead-codec.md and docs/backups.md);
	// kept identical so the whole stack agrees on one OWASP-recommended config.
	keyTime    = 1
	keyMem     = 64 * 1024 // 64 MiB
	keyThreads = 4

	// gcmTagLen is the authentication tag size of the AES-256-GCM construct
	// built by NewKeys/NewKeysFromKey (the cipher.NewGCM default for a 12-byte
	// nonce). Kept in one place so the sealed-size bookkeeping performed by
	// callers (the backup codec) stays exact for as long as this kernel defines
	// the blob format.
	gcmTagLen = 16
)

var (
	// ErrInvalidPassphrase is returned by Open when the sealed blob does not
	// authenticate under the current key, i.e. the passphrase is wrong.
	ErrInvalidPassphrase = errors.New("crypto: invalid passphrase")
	// ErrCorrupt is returned by Open when a blob looks sealed but is truncated,
	// has a bogus version, or is otherwise not a well-formed sealed blob.
	ErrCorrupt = errors.New("crypto: corrupt sealed data")
)

// Keys is an optional at-rest encryption key. A Keys with an empty passphrase
// is disabled: Seal returns its input unchanged and Open returns its input
// unchanged, so callers never need to branch on whether encryption is on. A
// Keys with a passphrase derives an AES-256 key via argon2id and seals/opens
// every payload with AES-256-GCM and a fresh random nonce.
type Keys struct {
	aead cipher.AEAD
	key  []byte // retained so an enabled Keys is never misused as disabled
}

// NewKeys derives an at-rest key from passphrase. An empty passphrase returns
// a disabled Keys (transparent passthrough); any other passphrase returns an
// enabled AES-256-GCM Keys. Derivation is CPU-bound (argon2id) and safe to do
// once per process at boot.
func NewKeys(passphrase string) *Keys {
	if passphrase == "" {
		return &Keys{}
	}
	salt := []byte(protocolSalt)
	key := argon2.IDKey([]byte(passphrase), salt, keyTime, keyMem, keyThreads, keyLen)
	return newKeys(key)
}

// NewKeysFromKey returns an enabled Keys that seals and opens with the given
// already-derived 32-byte AES-256 key, without running argon2id. It exists for
// callers that derive their own key (the backup codec, which carries a random
// per-backup salt and derives via argon2id before handing the key here) so they
// reuse the exact same sealed blob format, sentinels, and transparent-disabled
// behavior as passphrase-driven keys.
//
// A nil, empty, or non-32-byte key returns a disabled Keys (Seal/Open become
// transparent passthrough), mirroring NewKeys(""); callers can guard on
// Enabled() exactly once.
func NewKeysFromKey(key []byte) *Keys {
	if len(key) != keyLen {
		return &Keys{}
	}
	return newKeys(key)
}

// newKeys builds an enabled AES-256-GCM Keys from a validated 32-byte key.
func newKeys(key []byte) *Keys {
	block, err := aes.NewCipher(key)
	if err != nil {
		// AES-256 always succeeds for a 32-byte key; this is unreachable.
		panic(fmt.Sprintf("crypto: cannot create AES-256 cipher: %v", err))
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		// NewGCM only fails for a 2^n-1 nonce size; unreachable for 12 bytes.
		panic(fmt.Sprintf("crypto: cannot create GCM: %v", err))
	}
	return &Keys{aead: aead, key: key}
}

// SealHeaderLen returns the fixed byte length of a sealed blob's leading header
// (magic + version + nonce), before the GCM ciphertext+tag starts.
func SealHeaderLen() int { return blobHeaderLen + nonceLen }

// SealTagLen returns the byte length of the GCM authentication tag appended to
// each sealed blob.
func SealTagLen() int { return gcmTagLen }

// SealedSize returns the exact byte length of Seal(plain) for a plaintext of
// plainLen bytes, i.e. the storage a sealed artifact will occupy. It lets
// callers that need on-disk size bookkeeping (the backup codec's stored-size
// header) compute it without re-deriving the blob layout.
func SealedSize(plainLen int) int { return plainLen + SealHeaderLen() + SealTagLen() }

// Enabled reports whether this Keys encrypts at rest. A disabled Keys passes
// data through untouched (legacy / unencrypted behavior).
func (k *Keys) Enabled() bool { return k != nil && k.aead != nil }

// Seal encrypts plain, returning a self-describing sealed blob when enabled,
// or plain itself unchanged when disabled. The output of one call is
// guaranteed non-empty and safe to store directly in place of the plaintext.
func (k *Keys) Seal(plain []byte) []byte {
	if !k.Enabled() {
		return plain
	}
	nonce := make([]byte, nonceLen)
	if _, err := rand.Read(nonce); err != nil {
		panic(fmt.Sprintf("crypto: cannot read nonce: %v", err))
	}
	sealed := k.aead.Seal(nil, nonce, plain, nil)
	out := make([]byte, 0, blobHeaderLen+nonceLen+len(sealed))
	out = append(out, magic...)
	out = append(out, formatVersion)
	out = append(out, nonce...)
	out = append(out, sealed...)
	return out
}

// Open decrypts a blob produced by Seal. If the blob is not sealed (legacy
// plaintext, or encryption disabled) it is returned unchanged. A wrong
// passphrase yields ErrInvalidPassphrase; a malformed sealed blob yields
// ErrCorrupt.
func (k *Keys) Open(data []byte) ([]byte, error) {
	if !k.Enabled() {
		return data, nil
	}
	if !IsSealed(data) {
		// Legacy plaintext from before encryption was enabled; pass through.
		return data, nil
	}
	if len(data) < blobHeaderLen || len(data) < blobHeaderLen+nonceLen {
		return nil, ErrCorrupt
	}
	if data[blobHeaderLen-1] != formatVersion {
		return nil, ErrCorrupt
	}
	nonce := data[blobHeaderLen : blobHeaderLen+nonceLen]
	ct := data[blobHeaderLen+nonceLen:]
	open, err := k.aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, ErrInvalidPassphrase
	}
	return open, nil
}

// IsSealed reports whether data looks like a sealed at-rest blob (i.e. was
// produced by Seal with a non-empty key), as opposed to plaintext. It is used
// by tests to assert that artifacts are encrypted on disk and by Open to detect
// legacy plaintext.
func IsSealed(data []byte) bool {
	return bytes.HasPrefix(data, []byte(magic))
}
