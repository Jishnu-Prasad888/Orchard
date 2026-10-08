package crypto

import (
	"bytes"
	"testing"
)

func TestDisabledKeysAreTransparent(t *testing.T) {
	keys := NewKeys("")
	if keys.Enabled() {
		t.Fatal("empty passphrase must yield a disabled Keys")
	}
	plain := []byte("hello world")
	if got := keys.Seal(plain); !bytes.Equal(got, plain) {
		t.Error("disabled Seal must return input unchanged")
	}
	got, err := keys.Open(plain)
	if err != nil {
		t.Fatalf("disabled Open: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Error("disabled Open must return input unchanged")
	}
	if IsSealed(plain) {
		t.Error("disabled Keys must not mark plaintext as sealed")
	}
}

func TestEnabledKeysRoundTripAndSealIdentity(t *testing.T) {
	keys := NewKeys("test passphrase")
	if !keys.Enabled() {
		t.Fatal("non-empty passphrase must yield an enabled Keys")
	}
	plain := []byte("secret payload")
	sealed := keys.Seal(plain)
	if bytes.Equal(sealed, plain) {
		t.Error("enabled Seal must not equal the input")
	}
	if !IsSealed(sealed) {
		t.Error("enabled Seal output must be detectable as sealed")
	}
	opened, err := keys.Open(sealed)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(opened, plain) {
		t.Error("Open did not recover exact plaintext")
	}
}

func TestEnabledKeysRejectWrongPassphrase(t *testing.T) {
	sealer := NewKeys("correct passphrase")
	wrong := NewKeys("different passphrase")
	sealed := sealer.Seal([]byte("top secret"))
	if _, err := wrong.Open(sealed); err != ErrInvalidPassphrase {
		t.Fatalf("wrong passphrase: got %v, want ErrInvalidPassphrase", err)
	}
}

func TestEnabledKeysRejectCorruptBlob(t *testing.T) {
	keys := NewKeys("test")
	sealed := keys.Seal([]byte("payload"))

	// Truncated sealed blob.
	if _, err := keys.Open(sealed[:9]); err != ErrCorrupt {
		t.Fatalf("truncated: got %v, want ErrCorrupt", err)
	}
	// Bytes that merely carry the magic header but no ciphertext.
	bogus := append([]byte(magic), 'x')
	if _, err := keys.Open(bogus); err != ErrCorrupt {
		t.Fatalf("bogus header: got %v, want ErrCorrupt", err)
	}
	// Tampered ciphertext must fail authentication.
	tampered := append([]byte(nil), sealed...)
	tampered[len(tampered)-1] ^= 0x01
	if _, err := keys.Open(tampered); err == nil {
		t.Fatal("tampered blob opened without error")
	}
}

func TestEnabledKeysReadLegacyPlaintext(t *testing.T) {
	keys := NewKeys("test")
	legacy := []byte("plaintext written before encryption")
	got, err := keys.Open(legacy)
	if err != nil {
		t.Fatalf("Open legacy plaintext: %v", err)
	}
	if !bytes.Equal(got, legacy) {
		t.Error("legacy plaintext must pass through unchanged")
	}
}

func TestDeriveKeyStableAndDistinct(t *testing.T) {
	seed := []byte("payload")
	a := NewKeys("same passphrase").Seal(seed)
	b := NewKeys("same passphrase").Seal(seed)
	if bytes.Equal(a, b) {
		t.Error("two seals must use different nonces (randomization)")
	}
	keyOpenA, _ := NewKeys("same passphrase").Open(a)
	if !bytes.Equal(keyOpenA, seed) {
		t.Error("same passphrase must reproduce the key across instantiations")
	}
}

func TestNewKeysFromKeyDisabled(t *testing.T) {
	for _, key := range [][]byte{nil, {}, {0x01}, []byte("too-short-to-be-a-key")} {
		if keys := NewKeysFromKey(key); keys.Enabled() {
			t.Errorf("NewKeysFromKey(%v) must be disabled", key)
		}
	}
	disabled := NewKeysFromKey(nil)
	plain := []byte("opaque")
	if got := disabled.Seal(plain); !bytes.Equal(got, plain) {
		t.Error("disabled Seal must pass through")
	}
	if got, err := disabled.Open(plain); err != nil || !bytes.Equal(got, plain) {
		t.Error("disabled Open must pass through")
	}
}

func TestNewKeysFromKeyRoundTrip(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	keys := NewKeysFromKey(key)
	if !keys.Enabled() {
		t.Fatal("32-byte key must yield an enabled Keys")
	}
	plain := []byte("sealed backup chunk")
	sealed := keys.Seal(plain)
	if bytes.Equal(sealed, plain) {
		t.Error("enabled Seal must not equal the input")
	}
	if !IsSealed(sealed) {
		t.Error("sealed output must be detectable via IsSealed")
	}
	opened, err := keys.Open(sealed)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(opened, plain) {
		t.Error("Open did not recover exact plaintext")
	}
}

func TestNewKeysFromKeyWrongKey(t *testing.T) {
	sealKey := NewKeysFromKey([]byte("0123456789abcdef0123456789abcdef"))
	openKey := NewKeysFromKey([]byte("fedcba9876543210fedcba9876543210"))
	sealed := sealKey.Seal([]byte("sensitive payload"))
	if _, err := openKey.Open(sealed); err != ErrInvalidPassphrase {
		t.Fatalf("wrong key: got %v, want ErrInvalidPassphrase", err)
	}
}

func TestSealedSizeHelpers(t *testing.T) {
	if SealHeaderLen() != blobHeaderLen+nonceLen {
		t.Errorf("SealHeaderLen() = %d, want %d", SealHeaderLen(), blobHeaderLen+nonceLen)
	}
	if SealTagLen() != gcmTagLen {
		t.Errorf("SealTagLen() = %d, want %d", SealTagLen(), gcmTagLen)
	}
	keys := NewKeysFromKey([]byte("0123456789abcdef0123456789abcdef"))
	for _, n := range []int{0, 1, 63, 64, 1024, 65536} {
		plain := make([]byte, n)
		if got, want := len(keys.Seal(plain)), SealedSize(n); got != want {
			t.Errorf("SealedSize(%d) = %d, want %d (actual sealed length)", n, want, got)
		}
	}
}
