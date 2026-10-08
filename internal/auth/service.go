package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/Jishnu-Prasad888/Cairn/internal/audit"
)

// timingEqualizerHash is a valid argon2id hash used only to burn the same CPU
// budget on the login path for an unknown username as for a wrong password.
// Computed once on first use.
var timingEqualizerHash = sync.OnceValue(func() string {
	h, err := HashPassword("cairn-timing-equalizer-000")
	if err != nil {
		return ""
	}
	return h
})

// verifyPassword is indirection over VerifyPassword so tests can observe and
// stub the login-path hashing cost.
var verifyPassword = VerifyPassword

// Sentinel errors returned by the AuthService. Handlers map them to HTTP
// responses; none leak authentication details.
var (
	// ErrUnauthorized covers failed login and unknown, expired, revoked, or
	// user-disabled sessions. It is deliberately a single sentinel.
	ErrUnauthorized = errors.New("invalid credentials")
	// ErrConflict reports a unique violation: bootstrap already completed or a
	// username already taken.
	ErrConflict = errors.New("conflict")
	// ErrUserNotFound reports a requested user id that does not exist.
	ErrUserNotFound = errors.New("user not found")
)

// ValidationError describes a credential that failed validation rules.
type ValidationError struct{ Field, Reason string }

func (e *ValidationError) Error() string {
	return fmt.Sprintf("invalid %s: %s", e.Field, e.Reason)
}

// Meta holds non-sensitive context about the HTTP client, recorded with audit
// events and attached to new sessions for situational awareness.
type Meta struct {
	IPAddress string
	UserAgent string
}

// Service orchestrates authentication concerns against the server database.
type Service struct {
	db     *sql.DB
	logger *slog.Logger
	audit  *audit.Service
}

// NewService builds an authentication Service.
func NewService(db *sql.DB, logger *slog.Logger, audit *audit.Service) *Service {
	return &Service{db: db, logger: logger, audit: audit}
}

// UserCount returns the total number of user accounts.
func (s *Service) UserCount(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count users: %w", err)
	}
	return n, nil
}

// Bootstrap creates the initial administrator account and returns an
// authenticated session for it. It fails with ErrConflict once any user
// exists.
func (s *Service) Bootstrap(ctx context.Context, username, password string, meta Meta) (*User, string, error) {
	username = strings.TrimSpace(username)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, "", fmt.Errorf("begin bootstrap: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Conflict check runs before credential validation so a second bootstrap
	// always yields CONFLICT regardless of the submitted credentials.
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return nil, "", fmt.Errorf("count users: %w", err)
	}
	if n > 0 {
		return nil, "", ErrConflict
	}

	if err := ValidateCredentials(username, password); err != nil {
		return nil, "", err
	}

	hash, err := HashPassword(password)
	if err != nil {
		return nil, "", err
	}
	user := &User{
		ID:           newID(),
		Username:     username,
		PasswordHash: hash,
		Role:         RoleAdmin,
		Enabled:      true,
		CreatedAt:    time.Now().UTC(),
	}
	if err := s.insertUserTx(ctx, tx, user); err != nil {
		return nil, "", err
	}

	token, sess, err := s.createSessionTx(ctx, tx, user, meta)
	if err != nil {
		return nil, "", err
	}
	if err := tx.Commit(); err != nil {
		return nil, "", fmt.Errorf("commit bootstrap: %w", err)
	}

	s.audit.Record(ctx, audit.Event{
		ActorUserID: user.ID,
		Action:      audit.ActionBootstrapCompleted,
		IPAddress:   meta.IPAddress,
		UserAgent:   meta.UserAgent,
	})
	s.logger.Info("bootstrap completed", "user_id", user.ID, "username", user.Username, "session_id", sess.ID)
	return user, token, nil
}

// Login authenticates username+password and starts a session. Every failure
// surfaces the same ErrUnauthorized so responses never reveal which part was
// wrong.
func (s *Service) Login(ctx context.Context, username, password string, meta Meta) (*User, string, error) {
	user, err := s.userByUsername(ctx, strings.TrimSpace(username))
	if err != nil {
		// Equalize timing with the known-user path: evaluating a dummy argon2id
		// means "unknown username" costs the same as "wrong password", so
		// response time cannot be used to enumerate accounts.
		if h := timingEqualizerHash(); h != "" {
			_, _ = verifyPassword(password, h)
		}
		s.recordLoginFailure(ctx, strings.TrimSpace(username), meta)
		return nil, "", ErrUnauthorized
	}

	ok, err := verifyPassword(password, user.PasswordHash)
	if err != nil {
		s.logger.Warn("login: unparsable stored hash", "user_id", user.ID, "error", err)
		s.recordLoginFailure(ctx, user.Username, meta)
		return nil, "", ErrUnauthorized
	}
	if !ok || !user.Enabled {
		s.recordLoginFailure(ctx, user.Username, meta)
		return nil, "", ErrUnauthorized
	}

	token, sess, err := s.createSession(ctx, user, meta)
	if err != nil {
		return nil, "", err
	}
	s.audit.Record(ctx, audit.Event{
		ActorUserID: user.ID,
		Action:      audit.ActionLoginSucceeded,
		IPAddress:   meta.IPAddress,
		UserAgent:   meta.UserAgent,
	})
	s.logger.Info("login", "user_id", user.ID, "username", user.Username, "session_id", sess.ID)
	return user, token, nil
}

// Authenticate resolves an opaque session token to a live session + user. It
// fails with ErrUnauthorized for unknown, expired, revoked, or
// user-disabled sessions, administratively pruning dead sessions it finds.
func (s *Service) Authenticate(ctx context.Context, token string) (*Session, error) {
	if token == "" {
		return nil, ErrUnauthorized
	}
	const query = `
		SELECT s.id, s.created_at, s.expires_at, s.revoked_at,
		       u.id, u.username, u.password_hash, u.role, u.is_enabled, u.created_at
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = ?`
	row := s.db.QueryRowContext(ctx, query, hashToken(token))

	var (
		sess                           Session
		sessCreatedStr, sessExpiresStr string
		revokedAt                      sql.NullString
		u                              User
		role                           string
		enabled                        int
		userCreatedStr                 string
	)
	err := row.Scan(&sess.ID, &sessCreatedStr, &sessExpiresStr, &revokedAt,
		&u.ID, &u.Username, &u.PasswordHash, &role, &enabled, &userCreatedStr)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUnauthorized
	}
	if err != nil {
		return nil, fmt.Errorf("lookup session: %w", err)
	}
	if err := parseTime(sessCreatedStr, &sess.CreatedAt); err != nil {
		return nil, err
	}
	if err := parseTime(sessExpiresStr, &sess.ExpiresAt); err != nil {
		return nil, err
	}
	if err := parseTime(userCreatedStr, &u.CreatedAt); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	u.Role = Role(role)
	u.Enabled = enabled != 0

	if revokedAt.Valid || now.After(sess.ExpiresAt) || !u.Enabled {
		_ = s.deleteSession(ctx, sess.ID)
		return nil, ErrUnauthorized
	}

	sess.User = &u
	return &sess, nil
}

// Logout revokes the session identified by sessionID.
func (s *Service) Logout(ctx context.Context, sessionID string, meta Meta) {
	if sessionID == "" {
		return
	}
	_ = s.deleteSession(ctx, sessionID)
	s.audit.Record(ctx, audit.Event{
		ActorUserID: actorIDFrom(ctx),
		Action:      audit.ActionLogout,
		IPAddress:   meta.IPAddress,
		UserAgent:   meta.UserAgent,
	})
}

// CreateUser creates a non-admin-context user account with the given role and
// a validated password.
func (s *Service) CreateUser(ctx context.Context, username, password, role string) (*User, error) {
	if err := ValidateCredentials(username, password); err != nil {
		return nil, err
	}
	normalizedRole, err := NormalizeRole(role)
	if err != nil {
		return nil, err
	}

	hash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}
	user := &User{
		ID:           newID(),
		Username:     strings.TrimSpace(username),
		PasswordHash: hash,
		Role:         normalizedRole,
		Enabled:      true,
		CreatedAt:    time.Now().UTC(),
	}

	const query = `
		INSERT INTO users (id, username, password_hash, role, is_enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, 1, ?, ?)`
	if _, err := s.db.ExecContext(ctx, query,
		user.ID, user.Username, user.PasswordHash, string(user.Role),
		rfc3339(user.CreatedAt), rfc3339(user.CreatedAt)); err != nil {
		if isUniqueViolation(err) {
			return nil, ErrConflict
		}
		return nil, fmt.Errorf("insert user: %w", err)
	}

	s.audit.Record(ctx, audit.Event{
		ActorUserID:  actorIDFrom(ctx),
		TargetUserID: user.ID,
		Action:       audit.ActionUserCreated,
		Details:      map[string]any{"role": string(user.Role), "username": user.Username},
	})
	s.logger.Info("user created", "user_id", user.ID, "username", user.Username, "role", user.Role)
	return user, nil
}

// ListUsers returns all user accounts ordered by creation time.
func (s *Service) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, username, role, is_enabled, created_at
		FROM users ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var users []User
	for rows.Next() {
		var u User
		var role string
		var enabled int
		var createdStr string
		if err := rows.Scan(&u.ID, &u.Username, &role, &enabled, &createdStr); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		if err := parseTime(createdStr, &u.CreatedAt); err != nil {
			return nil, err
		}
		u.Role = Role(role)
		u.Enabled = enabled != 0
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate users: %w", err)
	}
	return users, nil
}

// RevokeUserSessions invalidates every session belonging to the user. It
// fails with ErrUserNotFound when the account does not exist.
func (s *Service) RevokeUserSessions(ctx context.Context, userID string, meta Meta) error {
	if userID == "" {
		return &ValidationError{Field: "id", Reason: "missing user id"}
	}
	if _, err := s.UserByID(ctx, userID); errors.Is(err, sql.ErrNoRows) {
		return ErrUserNotFound
	} else if err != nil {
		return fmt.Errorf("lookup user for revocation: %w", err)
	}

	const query = `UPDATE sessions SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL`
	res, err := s.db.ExecContext(ctx, query, rfc3339(time.Now().UTC()), userID)
	if err != nil {
		return fmt.Errorf("revoke user sessions: %w", err)
	}
	n, _ := res.RowsAffected()
	s.audit.Record(ctx, audit.Event{
		ActorUserID:  actorIDFrom(ctx),
		TargetUserID: userID,
		Action:       audit.ActionSessionRevoked,
		Details:      map[string]any{"revoked_sessions": n},
		IPAddress:    meta.IPAddress,
		UserAgent:    meta.UserAgent,
	})
	s.logger.Info("user sessions revoked", "user_id", userID, "sessions", n)
	return nil
}

// PruneExpired deletes expired sessions older than the given cutoff. Called at
// startup so the sessions table cannot grow without bound.
func (s *Service) PruneExpired(ctx context.Context, cutoff time.Time) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM sessions WHERE expires_at < ?`, rfc3339(cutoff)); err != nil {
		return fmt.Errorf("prune expired sessions: %w", err)
	}
	return nil
}

// UserByID returns a single user account, or sql.ErrNoRows.
func (s *Service) UserByID(ctx context.Context, id string) (*User, error) {
	const query = `SELECT id, username, password_hash, role, is_enabled, created_at FROM users WHERE id = ?`
	row := s.db.QueryRowContext(ctx, query, id)

	var u User
	var role string
	var enabled int
	var createdStr string
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &role, &enabled, &createdStr)
	if err != nil {
		return nil, err
	}
	if err := parseTime(createdStr, &u.CreatedAt); err != nil {
		return nil, err
	}
	u.Role = Role(role)
	u.Enabled = enabled != 0
	return &u, nil
}

// --- internals ---

func (s *Service) userByUsername(ctx context.Context, username string) (*User, error) {
	if username == "" {
		return nil, sql.ErrNoRows
	}
	const query = `SELECT id, username, password_hash, role, is_enabled, created_at FROM users WHERE username = ?`
	row := s.db.QueryRowContext(ctx, query, username)
	var u User
	var role string
	var enabled int
	var createdStr string
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &role, &enabled, &createdStr)
	if err != nil {
		return nil, err
	}
	if err := parseTime(createdStr, &u.CreatedAt); err != nil {
		return nil, err
	}
	u.Role = Role(role)
	u.Enabled = enabled != 0
	return &u, nil
}

func (s *Service) insertUserTx(ctx context.Context, tx *sql.Tx, u *User) error {
	const query = `
		INSERT INTO users (id, username, password_hash, role, is_enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`
	_, err := tx.ExecContext(ctx, query,
		u.ID, u.Username, u.PasswordHash, string(u.Role), 1,
		rfc3339(u.CreatedAt), rfc3339(u.CreatedAt))
	if err != nil {
		if isUniqueViolation(err) {
			return ErrConflict
		}
		return fmt.Errorf("insert user: %w", err)
	}
	return nil
}

func (s *Service) createSession(ctx context.Context, u *User, meta Meta) (string, *Session, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", nil, fmt.Errorf("begin session: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	token, sess, err := s.createSessionTx(ctx, tx, u, meta)
	if err != nil {
		return "", nil, err
	}
	if err := tx.Commit(); err != nil {
		return "", nil, fmt.Errorf("commit session: %w", err)
	}
	return token, sess, nil
}

func (s *Service) createSessionTx(ctx context.Context, tx *sql.Tx, u *User, meta Meta) (string, *Session, error) {
	token, digest, err := newSessionToken()
	if err != nil {
		return "", nil, err
	}
	now := time.Now().UTC()
	sess := &Session{
		ID:        newID(),
		User:      u,
		CreatedAt: now,
		ExpiresAt: now.Add(sessionTTL),
	}
	const query = `
		INSERT INTO sessions (id, user_id, token_hash, created_at, expires_at, user_agent, ip_address)
		VALUES (?, ?, ?, ?, ?, ?, ?)`
	if _, err := tx.ExecContext(ctx, query,
		sess.ID, u.ID, digest, rfc3339(sess.CreatedAt), rfc3339(sess.ExpiresAt),
		truncateString(meta.UserAgent, 256), truncateString(meta.IPAddress, 64)); err != nil {
		return "", nil, fmt.Errorf("insert session: %w", err)
	}
	return token, sess, nil
}

func (s *Service) deleteSession(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

func (s *Service) recordLoginFailure(ctx context.Context, username string, meta Meta) {
	s.audit.Record(ctx, audit.Event{
		Action:    audit.ActionLoginFailed,
		Details:   map[string]any{"username": truncateString(username, 64)},
		IPAddress: meta.IPAddress,
		UserAgent: meta.UserAgent,
	})
}

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("id-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func parseTime(value string, out *time.Time) error {
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return fmt.Errorf("parse stored timestamp %q: %w", value, err)
	}
	*out = t
	return nil
}

func truncateString(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

func isUniqueViolation(err error) bool {
	// modernc.org/sqlite surfaces constraint violations as errors whose text
	// contains "UNIQUE constraint failed". Matching on the message is fragile
	// but sufficient here: a failed insert is logged and retries surface the
	// same behavior. Text matching stays local to this helper.
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// actorIDFrom returns the acting user id from the context, if the caller
// stored one. Package-level helper kept small; the HTTP layer populates it.
func actorIDFrom(ctx context.Context) string {
	if v, ok := ctx.Value(userContextKey{}).(*User); ok && v != nil {
		return v.ID
	}
	return ""
}

type userContextKey struct{}
