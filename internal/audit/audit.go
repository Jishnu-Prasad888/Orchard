// Package audit records structured, tamper-proof-by-construction events about
// important application activity: authentication, user management, and later
// permissions, sharing, and backups.
//
// Audit records never contain credentials, session tokens, share tokens, media
// content, or other sensitive material. Call sites choose which structured,
// non-sensitive details to attach.
package audit

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

// Action is a stable machine-readable label for an audited event. New actions
// are added as new features land; existing labels must not change because they
// may appear in exports or logs.
const (
	ActionBootstrapCompleted = "auth.bootstrap_completed"
	ActionLoginSucceeded     = "auth.login"
	ActionLoginFailed        = "auth.login_failed"
	ActionLogout             = "auth.logout"
	ActionSessionRevoked     = "auth.session_revoked"
	ActionUserCreated        = "users.created"
	ActionUserUpdated        = "users.updated"
	ActionUserDisabled       = "users.disabled"
	ActionUserEnabled        = "users.enabled"
	ActionLibraryCreated     = "libraries.created"
	ActionLibraryAdopted     = "libraries.adopted"
	ActionLibraryRefreshed   = "libraries.refreshed"
	ActionLibraryDeleted     = "libraries.deleted"
	ActionGrantCreated       = "authz.grant_created"
	ActionGrantRevoked       = "authz.grant_revoked"
	ActionShareCreated       = "authz.share_created"
	ActionShareRevoked       = "authz.share_revoked"
)

// Event is one audit record ready for persistence.
type Event struct {
	ActorUserID  string         // principal performing the action; empty for unauthenticated actors.
	Action       string         // one of the Action constants.
	TargetUserID string         // optional user the action concerns.
	IPAddress    string         // best-effort actor address; never sensitive.
	UserAgent    string         // best-effort client agent; truncated and never persisted raw.
	Details      map[string]any // extra structured, non-sensitive context.
}

// Service writes audit records to the server database. Writes are best-effort:
// a failing audit must not take down the primary operation it describes.
type Service struct {
	db     *sql.DB
	logger *slog.Logger
}

// New returns an audit Service that writes records to db. Backing off the
// server after recording every event would make authentication fragile, so
// failures are logged and swallowed.
func New(db *sql.DB, logger *slog.Logger) *Service {
	return &Service{db: db, logger: logger}
}

// Record persists an event. Logging failures are reported through the
// structured logger but never returned to the caller as errors.
func (s *Service) Record(ctx context.Context, e Event) {
	details, err := json.Marshal(e.Details)
	if err != nil {
		s.logger.Error("audit: serialize details", "error", err)
		details = []byte("{}")
	}

	userAgent := truncate(e.UserAgent, 256)
	const query = `
		INSERT INTO audit_log (
			id, user_id, action, target_user_id, metadata, ip_address, user_agent, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	_, err = s.db.ExecContext(ctx, query,
		randID(16), nullableString(e.ActorUserID), e.Action,
		nullableString(e.TargetUserID), string(details),
		truncate(e.IPAddress, 64), userAgent,
		time.Now().UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		s.logger.Error("audit: record event", "action", e.Action, "error", err)
	}
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

// randID returns n random bytes hex-encoded. Should entropy generation ever
// fail, the record still receives a unique-enough fallback identifier so the
// audit log is never silently dropped.
func randID(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("fallback-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
