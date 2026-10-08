package auth

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"golang.org/x/crypto/argon2"
)

func TestHashAndVerifyPassword(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	ok, err := VerifyPassword("correct horse battery staple", hash)
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if !ok {
		t.Fatal("expected password to verify")
	}

	ok, err = VerifyPassword("wrong password", hash)
	if err != nil {
		t.Fatalf("VerifyPassword (wrong): %v", err)
	}
	if ok {
		t.Fatal("wrong password must not verify")
	}
}

func TestHashesAreSalted(t *testing.T) {
	h1, err := HashPassword("same password")
	if err != nil {
		t.Fatal(err)
	}
	h2, err := HashPassword("same password")
	if err != nil {
		t.Fatal(err)
	}
	if h1 == h2 {
		t.Fatal("two hashes of the same password must differ (unique salt)")
	}
}

func TestVerifyPasswordRejectsMalformedHash(t *testing.T) {
	for _, bad := range []string{
		"",
		"$argon2i$v=19$m=65536,t=3,p=2$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=65536,t=3,p=2$c2FsdA",
		"not-a-phc-hash",
	} {
		if _, err := VerifyPassword("pw", bad); err == nil {
			t.Errorf("VerifyPassword(%q): expected error, got nil", bad)
		}
	}
}

func TestVerifyPasswordRejectsCorruptedKey(t *testing.T) {
	hash, err := HashPassword("pw")
	if err != nil {
		t.Fatal(err)
	}
	// Corrupt a byte inside the stored key and rebuild the PHC string. Flipping
	// a bit guarantees the key actually changes (replacing a trailing character
	// can be a no-op when the base64 encoding happens to end with that exact
	// character, which made this test flaky ~1/64 runs).
	salt, params, key, err := parsePhc(hash)
	if err != nil {
		t.Fatal(err)
	}
	key[0] ^= 0x01
	corrupted := fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, params.memory, params.time, params.threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	)
	ok, err := VerifyPassword("pw", corrupted)
	if err != nil {
		t.Fatalf("VerifyPassword corrupt: %v", err)
	}
	if ok {
		t.Fatal("corrupted hash must not verify")
	}
}

func TestParsePhcParamsRoundtrip(t *testing.T) {
	hash, err := HashPassword("pw")
	if err != nil {
		t.Fatal(err)
	}
	salt, params, key, err := parsePhc(hash)
	if err != nil {
		t.Fatalf("parsePhc: %v", err)
	}
	if len(salt) != argonSaltLen {
		t.Errorf("salt length = %d, want %d", len(salt), argonSaltLen)
	}
	if len(key) != argonKeyLen {
		t.Errorf("key length = %d, want %d", len(key), argonKeyLen)
	}
	if params.memory != argonMemory || params.time != argonTime || params.threads != argonThreads {
		t.Errorf("params = %+v, want m=%d t=%d p=%d", params, argonMemory, argonTime, argonThreads)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$") {
		t.Errorf("hash does not start with argon2id v19 marker: %q", hash)
	}
}
