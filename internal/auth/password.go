// Package auth implements Cairn authentication: user accounts with argon2id
// password hashing, opaque session tokens stored only as hashes, roles, and
// the login/logout/current-user flows.
//
// Authentication and authorization are deliberately separate packages.
// Authentication answers "who is the actor?"; the resource-based permission
// subsystem (a later phase) answers "what may they do?". Role checks here are
// an initial convenience layer and are centralized rather than scattered.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters. These follow current OWASP guidance for argon2id and
// are tuned to balance cost on low-power devices (Raspberry Pi) with
// resistance to offline guessing. Hashing happens only at login and user
// creation, so the cost is rarely exercised.
const (
	argonTime    = 3
	argonMemory  = 64 * 1024 // KiB (64 MiB)
	argonThreads = 2
	argonKeyLen  = 32
	argonSaltLen = 16
)

// Limits applied to credentials before hashing, so the server cannot be used
// as a memory/CPU sink through absurdly large inputs.
const (
	maxPasswordBytes = 1024
	minPasswordBytes = 8
)

// HashPassword returns an argon2id PHC-formatted hash of the password.
// Exported for reuse by subsystems that must protect secrets (share
// passwords); the hashing configuration lives here, in one place.
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// VerifyPassword checks password against an argon2id PHC hash using the
// parameters recorded in the hash itself (so parameters can evolve without
// invalidating existing accounts).
func VerifyPassword(password, encoded string) (bool, error) {
	salt, params, key, err := parsePhc(encoded)
	if err != nil {
		return false, err
	}
	other := argon2.IDKey([]byte(password), salt, params.time, params.memory, uint8(params.threads), uint32(len(key)))
	return subtle.ConstantTimeCompare(other, key) == 1, nil
}

type phcParams struct{ time, memory, threads uint32 }

// parsePhc extracts salt, parameters, and the derived key from an argon2id PHC
// string. It accepts the format emitted by hashPassword.
func parsePhc(encoded string) ([]byte, phcParams, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return nil, phcParams{}, nil, errors.New("malformed argon2id hash")
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return nil, phcParams{}, nil, fmt.Errorf("parse argon2 version: %w", err)
	}

	var p phcParams
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memory, &p.time, &p.threads); err != nil {
		return nil, phcParams{}, nil, fmt.Errorf("parse argon2 parameters: %w", err)
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return nil, phcParams{}, nil, fmt.Errorf("decode salt: %w", err)
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return nil, phcParams{}, nil, fmt.Errorf("decode hash: %w", err)
	}
	return salt, p, key, nil
}
