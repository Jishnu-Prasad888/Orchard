package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"time"
)

// Session lifetime. Sessions are renewed on login; a fixed generous TTL keeps
// self-hosted use convenient while expiry still bounds exposure.
const sessionTTL = 30 * 24 * time.Hour

// tokenLenBytes is the entropy of an opaque session token (256 bits).
const tokenLenBytes = 32

// newSessionToken returns a fresh opaque 256-bit token, base64url encoded, and
// its SHA-256 hex digest for storage. The raw token is given to the client
// exactly once (the session cookie); only the digest is ever persisted.
func newSessionToken() (token string, digest string, err error) {
	raw := make([]byte, tokenLenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("generate session token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	return token, hashToken(token), nil
}

// hashToken returns the hex SHA-256 digest used to store and look up tokens.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
