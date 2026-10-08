package authz

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Jishnu-Prasad888/Cairn/internal/audit"
	"github.com/Jishnu-Prasad888/Cairn/internal/auth"
	"github.com/Jishnu-Prasad888/Cairn/internal/likeutil"
)

// NewShareInput describes an intended public share.
type NewShareInput struct {
	// ResourceKey is the resource subtree to expose.
	ResourceKey string
	// Caps are the capabilities the share grants. Caps are always evaluated
	// through the same policy as everything else; a share never bypasses
	// authorization, it only adds an explicit grant rooted at ResourceKey.
	Caps CapSet
	// Password, when non-empty, requires the share holder to present it.
	Password string
	// ExpiresAt, when set, disables the share after the given instant.
	ExpiresAt *time.Time
	// CreatedBy is the acting user id for audit purposes.
	CreatedBy string
}

// ShareView is a share returned to callers. Token is populated only on
// creation; no code path ever re-derives it from storage.
type ShareView struct {
	Share
	Token string
}

// CreateShare mints a public share rooted at a resource. The raw token is
// returned exactly once; only its SHA-256 is persisted. A share grants its
// capabilities on the resource and, via prefix inheritance, its descendants —
// through the exact same evaluation used for user grants.
func (s *Service) CreateShare(ctx context.Context, in NewShareInput) (*ShareView, error) {
	if in.ResourceKey == "" {
		return nil, &ValidationError{Field: "resource_key", Reason: "must not be empty"}
	}
	if len(in.Caps) == 0 {
		return nil, &ValidationError{Field: "capabilities", Reason: "must not be empty"}
	}
	if len(in.Password) > 0 && len(in.Password) < 8 {
		return nil, &ValidationError{Field: "password", Reason: "must be at least 8 characters when set"}
	}

	token, digest, err := newShareToken()
	if err != nil {
		return nil, err
	}

	pwHash := sql.NullString{}
	if in.Password != "" {
		hash, err := auth.HashPassword(in.Password)
		if err != nil {
			return nil, err
		}
		pwHash = sql.NullString{String: hash, Valid: true}
	}

	now := time.Now().UTC()
	sh := Share{
		ID:           newID(),
		ResourceKey:  in.ResourceKey,
		Capabilities: cloneCaps(in.Caps),
		TokenHash:    digest,
		PasswordHash: pwHash,
		CreatedBy:    in.CreatedBy,
		CreatedAt:    now,
	}
	if in.ExpiresAt != nil {
		exp := in.ExpiresAt.UTC()
		sh.ExpiresAt = &exp
	}

	const query = `
		INSERT INTO shares (id, resource_key, capabilities, token_hash, password_hash, expires_at, revoked_at, created_by, created_at)
		VALUES (?, ?, ?, ?, ?, ?, NULL, ?, ?)`
	if _, err := s.db.ExecContext(ctx, query,
		sh.ID, sh.ResourceKey, capCSV(sh.Capabilities), sh.TokenHash,
		pwHash, nullableTime(sh.ExpiresAt),
		sh.CreatedBy, rfc3339(sh.CreatedAt)); err != nil {
		return nil, fmt.Errorf("insert share: %w", err)
	}

	if s.audit != nil {
		s.audit.Record(ctx, audit.Event{
			ActorUserID: in.CreatedBy,
			Action:      audit.ActionShareCreated,
			Details: map[string]any{
				"share_id":     sh.ID,
				"resource_key": sh.ResourceKey,
				"capabilities": strings.Join(capStrings(sh.Capabilities), ","),
				"password":     in.Password != "",
				"expires_at":   nullableTimeString(sh.ExpiresAt),
			},
		})
	}
	s.logger.Info("share created", "share_id", sh.ID, "resource_key", sh.ResourceKey)

	return &ShareView{Share: sh, Token: token}, nil
}

// AuthenticateShare resolves a raw share token to its live share, enforcing
// revocation, expiry, and (when set) the password. It returns ErrUnauthorized
// for unknown, revoked, expired, or wrong-password shares; it never reveals
// which condition failed.
func (s *Service) AuthenticateShare(ctx context.Context, token, password string) (*Share, error) {
	if token == "" {
		return nil, ErrUnauthorized
	}
	sh, err := s.shareByTokenHash(ctx, hashToken(token))
	if errors.Is(err, ErrNotFound) {
		return nil, ErrUnauthorized
	}
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	if sh.RevokedAt != nil && !sh.RevokedAt.IsZero() {
		return nil, ErrUnauthorized
	}
	if sh.ExpiresAt != nil && !sh.ExpiresAt.IsZero() && now.After(*sh.ExpiresAt) {
		return nil, ErrUnauthorized
	}
	if sh.PasswordHash.Valid {
		ok, err := auth.VerifyPassword(password, sh.PasswordHash.String)
		if err != nil || !ok {
			return nil, ErrUnauthorized
		}
	}
	return sh, nil
}

// ListShares returns the shares rooted at the given key prefix, oldest first.
func (s *Service) ListShares(ctx context.Context, keyPrefix string) ([]Share, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, resource_key, capabilities, token_hash, password_hash, expires_at, revoked_at, created_by, created_at
		FROM shares
		WHERE resource_key = ? OR resource_key LIKE ? `+likeutil.EscapeClause+`
		ORDER BY created_at ASC`, keyPrefix, likeutil.Escape(keyPrefix+keySep)+"%")
	if err != nil {
		return nil, fmt.Errorf("list shares: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Share
	for rows.Next() {
		sh, err := scanShare(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sh)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate shares: %w", err)
	}
	return out, nil
}

// RevokeShare permanently disables a share. The token becomes unusable
// immediately; share access never survives revocation.
func (s *Service) RevokeShare(ctx context.Context, shareID, actorID string) error {
	if shareID == "" {
		return &ValidationError{Field: "share_id", Reason: "must not be empty"}
	}
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx,
		`UPDATE shares SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`, rfc3339(now), shareID)
	if err != nil {
		return fmt.Errorf("revoke share: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}

	if s.audit != nil {
		s.audit.Record(ctx, audit.Event{
			ActorUserID: actorID,
			Action:      audit.ActionShareRevoked,
			Details:     map[string]any{"share_id": shareID},
		})
	}
	s.logger.Info("share revoked", "share_id", shareID)
	return nil
}

// shareByTokenHash loads a share by the SHA-256 digest of its raw token.
func (s *Service) shareByTokenHash(ctx context.Context, digest string) (*Share, error) {
	return scanShare(s.db.QueryRowContext(ctx, `
		SELECT id, resource_key, capabilities, token_hash, password_hash, expires_at, revoked_at, created_by, created_at
		FROM shares WHERE token_hash = ?`, digest))
}

// ShareByID returns a single share by id, or ErrNotFound.
func (s *Service) ShareByID(ctx context.Context, shareID string) (*Share, error) {
	return scanShare(s.db.QueryRowContext(ctx, `
		SELECT id, resource_key, capabilities, token_hash, password_hash, expires_at, revoked_at, created_by, created_at
		FROM shares WHERE id = ?`, shareID))
}

// scanShare scans one share row.
func scanShare(row rowScanner) (*Share, error) {
	var (
		sh         Share
		capsCSV    string
		expiresStr sql.NullString
		revokedStr sql.NullString
		createdStr string
	)
	err := row.Scan(&sh.ID, &sh.ResourceKey, &capsCSV, &sh.TokenHash, &sh.PasswordHash,
		&expiresStr, &revokedStr, &sh.CreatedBy, &createdStr)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scan share: %w", err)
	}
	set, err := capSetFromStrings(strings.Split(capsCSV, ","))
	if err != nil {
		return nil, fmt.Errorf("corrupt share capabilities %q: %w", capsCSV, err)
	}
	sh.Capabilities = set
	if t, err := parseTime(expiresStr.String); err != nil {
		return nil, err
	} else if expiresStr.Valid && !t.IsZero() {
		sh.ExpiresAt = &t
	}
	if t, err := parseTime(revokedStr.String); err != nil {
		return nil, err
	} else if revokedStr.Valid && !t.IsZero() {
		sh.RevokedAt = &t
	}
	if sh.CreatedAt, err = parseTime(createdStr); err != nil {
		return nil, err
	}
	return &sh, nil
}

// rowScanner is satisfied by database/sql Row and Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func newShareToken() (string, string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("generate share token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	return token, hashToken(token), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func nullableTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return rfc3339(*t)
}

func nullableTimeString(t *time.Time) any {
	return nullableTime(t)
}
