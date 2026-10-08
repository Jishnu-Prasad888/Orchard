package auth

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Role is the initial account-level role on a user. Remember: roles are a
// convenience layer on top of the (later) resource-based permission model,
// never the fundamental authorization unit.
type Role string

const (
	// RoleAdmin has full administrative authority over the server instance.
	RoleAdmin Role = "admin"
	// RoleUser is a regular authenticated user.
	RoleUser Role = "user"
)

// String returns the machine representation of the role.
func (r Role) String() string { return string(r) }

// valid reports whether exactly one of admin/user was specified (mirrors the
// CHECK constraint in the users table).
func (r Role) valid() bool { return r == RoleAdmin || r == RoleUser }

// User is an authenticated principal. PasswordHash is never serialized to
// clients.
type User struct {
	ID           string
	Username     string
	PasswordHash string
	Role         Role
	Enabled      bool
	CreatedAt    time.Time
}

// Session is an authenticated session bound to a user.
type Session struct {
	ID        string
	User      *User
	CreatedAt time.Time
	ExpiresAt time.Time
}

var usernameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$`)

// ValidateCredentials applies the shared user-creation rules and returns a
// ValidationError describing the first problem found.
func ValidateCredentials(username, password string) error {
	username = strings.TrimSpace(username)
	if !usernameRe.MatchString(username) {
		return &ValidationError{Field: "username",
			Reason: "must be 1-64 characters of letters, digits, '.', '_' or '-', starting with a letter or digit"}
	}
	if len(password) < minPasswordBytes {
		return &ValidationError{Field: "password", Reason: fmt.Sprintf("must be at least %d bytes", minPasswordBytes)}
	}
	if len(password) > maxPasswordBytes {
		return &ValidationError{Field: "password", Reason: fmt.Sprintf("must be at most %d bytes", maxPasswordBytes)}
	}
	return nil
}

// NormalizeRole folds a requested role to a Role value, rejecting anything
// that is not admin or user.
func NormalizeRole(r string) (Role, error) {
	role := Role(strings.ToLower(strings.TrimSpace(r)))
	if !role.valid() {
		return "", &ValidationError{Field: "role", Reason: `must be "admin" or "user"`}
	}
	return role, nil
}
