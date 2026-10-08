package auth

import (
	"crypto/subtle"
	"testing"
)

func TestNewSessionTokenIsLargeAndOpaque(t *testing.T) {
	for i := 0; i < 10; i++ {
		token, digest, err := newSessionToken()
		if err != nil {
			t.Fatalf("newSessionToken: %v", err)
		}
		// 32 bytes -> 43 chars in base64url (no padding).
		if len(token) != 43 {
			t.Errorf("token length = %d, want 43", len(token))
		}
		for _, r := range token {
			switch {
			case r >= 'A' && r <= 'Z',
				r >= 'a' && r <= 'z',
				r >= '0' && r <= '9',
				r == '-', r == '_':
			default:
				t.Errorf("token contains non-base64url character %q", r)
			}
		}
		if digest != hashToken(token) {
			t.Fatal("digest is not the hash of the token")
		}
	}
}

func TestNewSessionTokensAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 64; i++ {
		token, _, err := newSessionToken()
		if err != nil {
			t.Fatal(err)
		}
		if seen[token] {
			t.Fatal("duplicate token generated")
		}
		seen[token] = true
	}
}

func TestHashTokenDeterministic(t *testing.T) {
	a, b := hashToken("token-a"), hashToken("token-a")
	if a != b {
		t.Fatal("same token must hash identically")
	}
	if subtle.ConstantTimeCompare([]byte(a), []byte(hashToken("token-b"))) == 1 {
		t.Fatal("different tokens must hash differently")
	}
}
