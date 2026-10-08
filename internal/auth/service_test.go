package auth

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/Jishnu-Prasad888/Cairn/internal/audit"
	"github.com/Jishnu-Prasad888/Cairn/internal/db"
)

// newTestService opens a fresh migrated server database and returns an auth
// Service wired to it.
func TestLoginUnknownUsernameRunsVerifier(t *testing.T) {
	svc, _ := newTestService(t)
	_ = svc

	// The dummy-verify path should invoke VerifyPassword even though the
	// username does not exist, so timing does not leak account existence.
	original := verifyPassword
	calls := 0
	verifyPassword = func(password, encoded string) (bool, error) {
		calls++
		return false, nil
	}
	defer func() { verifyPassword = original }()

	if _, _, err := svc.Login(context.Background(), "ghost", "any", Meta{}); err != ErrUnauthorized {
		t.Fatalf("Login(unknown) err = %v, want ErrUnauthorized", err)
	}
	if calls != 1 {
		t.Errorf("VerifyPassword invoked %d times for unknown user, want 1", calls)
	}
}

func newTestService(t *testing.T) (*Service, *sql.DB) {
	t.Helper()
	pool, err := db.Open(filepath.Join(t.TempDir(), "cairn.db"))
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewService(pool, logger, audit.New(pool, logger)), pool
}

func mustBootstrap(t *testing.T, svc *Service) (user *User, token string) {
	t.Helper()
	user, token, err := svc.Bootstrap(context.Background(), "admin", "correct-horse-battery", Meta{})
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	return user, token
}

func TestBootstrapCreatesAdmin(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	user, token, err := svc.Bootstrap(ctx, "admin", "correct-horse-battery", Meta{})
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if user.Role != RoleAdmin {
		t.Errorf("role = %q, want admin", user.Role)
	}
	if user.Username != "admin" {
		t.Errorf("username = %q, want admin", user.Username)
	}

	sess, err := svc.Authenticate(ctx, token)
	if err != nil {
		t.Fatalf("session from bootstrap token: %v", err)
	}
	if sess.User.ID != user.ID {
		t.Errorf("session user id = %q, want %q", sess.User.ID, user.ID)
	}
}

func TestBootstrapRejectsSecondCall(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	mustBootstrap(t, svc)

	if _, _, err := svc.Bootstrap(ctx, "other", "password123", Meta{}); err == nil {
		t.Fatal("second bootstrap must fail")
	} else if err != ErrConflict {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
}

func TestBootstrapRejectsInvalidCredentials(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	for _, tc := range []struct {
		username, password string
	}{
		{"", "password123"},
		{"a", ""},
		{"bad name!", "password123"},
		{"admin", "short"},
	} {
		if _, _, err := svc.Bootstrap(ctx, tc.username, tc.password, Meta{}); err == nil {
			t.Errorf("Bootstrap(%q, %q) expected error", tc.username, "…")
		}
	}
}

func TestLoginSuccessAndFailure(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	mustBootstrap(t, svc)

	user, token, err := svc.Login(ctx, "admin", "correct-horse-battery", Meta{})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if user.Role != RoleAdmin {
		t.Errorf("role = %q, want admin", user.Role)
	}
	if sess, err := svc.Authenticate(ctx, token); err != nil || sess == nil {
		t.Fatalf("session after login: %v", err)
	}

	// Wrong password and unknown username must both surface ErrUnauthorized.
	for _, tc := range []struct{ u, p string }{
		{"admin", "wrong-password"},
		{"ghost", "correct-horse-battery"},
	} {
		if _, _, err := svc.Login(ctx, tc.u, tc.p, Meta{}); err != ErrUnauthorized {
			t.Errorf("Login(%q): err = %v, want ErrUnauthorized", tc.u, err)
		}
	}
}

func TestAuthenticateRejectsUnknownToken(t *testing.T) {
	svc, _ := newTestService(t)
	if _, err := svc.Authenticate(context.Background(), "not-a-real-token"); err != ErrUnauthorized {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
	if _, err := svc.Authenticate(context.Background(), ""); err != ErrUnauthorized {
		t.Fatalf("empty token err = %v, want ErrUnauthorized", err)
	}
}

func TestSessionRevokedByLogout(t *testing.T) {
	svc, pool := newTestService(t)
	ctx := context.Background()
	mustBootstrap(t, svc)

	_, token := mustLogin(t, svc)
	sess, err := svc.Authenticate(ctx, token)
	if err != nil {
		t.Fatalf("authenticate before logout: %v", err)
	}

	svc.Logout(ctx, sess.ID, Meta{})
	if _, err := svc.Authenticate(ctx, token); err != ErrUnauthorized {
		t.Fatalf("authenticate after logout: err = %v, want ErrUnauthorized", err)
	}

	// The revoked session and its token digest are gone from the database.
	var n int
	if err := pool.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id = ?`, sess.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("session row still present after logout (n=%d)", n)
	}
}

func TestExpiredSessionRejected(t *testing.T) {
	svc, pool := newTestService(t)
	ctx := context.Background()
	mustBootstrap(t, svc)
	_, token := mustLogin(t, svc)
	digest := hashToken(token)

	// Force the session to be expired.
	if _, err := pool.Exec(`UPDATE sessions SET expires_at = ? WHERE token_hash = ?`,
		rfc3339(time.Now().UTC().Add(-time.Hour)), digest); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authenticate(ctx, token); err != ErrUnauthorized {
		t.Fatalf("expired session: err = %v, want ErrUnauthorized", err)
	}
}

func TestDisabledUserCannotAuthenticate(t *testing.T) {
	svc, pool := newTestService(t)
	ctx := context.Background()
	mustBootstrap(t, svc)
	_, token := mustLogin(t, svc)

	if _, err := pool.Exec(`UPDATE users SET is_enabled = 0`); err != nil {
		t.Fatal(err)
	}
	// A live session for a now-disabled user is inert.
	if _, err := svc.Authenticate(ctx, token); err != ErrUnauthorized {
		t.Fatalf("disabled user session: err = %v, want ErrUnauthorized", err)
	}
	// And they cannot log in again.
	if _, _, err := svc.Login(ctx, "admin", "correct-horse-battery", Meta{}); err != ErrUnauthorized {
		t.Fatalf("disabled login: err = %v, want ErrUnauthorized", err)
	}
}

func TestCreateUserDuplicatesAndValidation(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	mustBootstrap(t, svc)

	u, err := svc.CreateUser(ctx, "alice", "s3cret-password", string(RoleUser))
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if u.Role != RoleUser {
		t.Errorf("role = %q, want user", u.Role)
	}

	if _, err := svc.CreateUser(ctx, "alice", "another-password", string(RoleUser)); err != ErrConflict {
		t.Fatalf("duplicate username: err = %v, want ErrConflict", err)
	}
	// Case-insensitive uniqueness comes from the NOCASE collation.
	if _, err := svc.CreateUser(ctx, "ALICE", "another-password", string(RoleUser)); err != ErrConflict {
		t.Fatalf("case-variant duplicate: err = %v, want ErrConflict", err)
	}
	if _, err := svc.CreateUser(ctx, "bob", "s3cret-password", "owner"); err == nil {
		t.Fatal("invalid role must fail")
	}
	if _, err := svc.CreateUser(ctx, "bob", "s3cret-password", string(RoleAdmin)); err != nil {
		t.Fatalf("create admin: %v", err)
	}
}

func TestRevokeUserSessions(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	mustBootstrap(t, svc)

	users, err := svc.ListUsers(ctx)
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	var adminID string
	for i := range users {
		if users[i].Role == RoleAdmin {
			adminID = users[i].ID
		}
	}
	if adminID == "" {
		t.Fatal("admin user not found")
	}

	alice, err := svc.CreateUser(ctx, "alice", "s3cret-password", string(RoleUser))
	if err != nil {
		t.Fatal(err)
	}
	_, aliceToken1 := mustLoginUser(t, svc, "alice", "s3cret-password")
	_, aliceToken2 := mustLoginUser(t, svc, "alice", "s3cret-password")

	if err := svc.RevokeUserSessions(ctx, alice.ID, Meta{}); err != nil {
		t.Fatalf("RevokeUserSessions: %v", err)
	}
	for i, token := range []string{aliceToken1, aliceToken2} {
		if _, err := svc.Authenticate(ctx, token); err != ErrUnauthorized {
			t.Errorf("alice token %d still authenticates after revocation (err=%v)", i, err)
		}
	}
	// Admin session survives.
	if err := svc.RevokeUserSessions(ctx, adminID, Meta{}); err != nil {
		t.Fatal(err)
	}

	// Unknown target user.
	if err := svc.RevokeUserSessions(ctx, "does-not-exist", Meta{}); err != ErrUserNotFound {
		t.Fatalf("err = %v, want ErrUserNotFound", err)
	}
}

func TestPruneExpiredRemovesOldSessions(t *testing.T) {
	svc, pool := newTestService(t)
	ctx := context.Background()
	mustBootstrap(t, svc)

	mustLogin(t, svc)
	// Bootstrapping and logging in each created a session; expire them all.
	if _, err := pool.Exec(`UPDATE sessions SET expires_at = ?`,
		rfc3339(time.Now().UTC().Add(-time.Hour))); err != nil {
		t.Fatal(err)
	}
	if err := svc.PruneExpired(ctx, time.Now().UTC()); err != nil {
		t.Fatalf("PruneExpired: %v", err)
	}

	var n int
	if err := pool.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("expired sessions not pruned (n=%d)", n)
	}
}

func TestAuditEventsAreRecorded(t *testing.T) {
	svc, pool := newTestService(t)
	ctx := context.Background()
	mustBootstrap(t, svc)
	mustLogin(t, svc)
	_, _, _ = svc.Login(ctx, "admin", "wrong-password", Meta{IPAddress: "127.0.0.1"})

	var n int
	if err := pool.QueryRow(`SELECT COUNT(*) FROM audit_log`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	// bootstrap + login success + login failure.
	if n < 3 {
		t.Errorf("audit rows = %d, want at least 3", n)
	}

	var loginFailed int
	if err := pool.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = ?`,
		audit.ActionLoginFailed).Scan(&loginFailed); err != nil {
		t.Fatal(err)
	}
	if loginFailed != 1 {
		t.Errorf("login_failed rows = %d, want 1", loginFailed)
	}
}

func mustLogin(t *testing.T, svc *Service) (*User, string) {
	t.Helper()
	return mustLoginUser(t, svc, "admin", "correct-horse-battery")
}

func mustLoginUser(t *testing.T, svc *Service, username, password string) (*User, string) {
	t.Helper()
	user, token, err := svc.Login(context.Background(), username, password, Meta{})
	if err != nil {
		t.Fatalf("Login(%q): %v", username, err)
	}
	return user, token
}
