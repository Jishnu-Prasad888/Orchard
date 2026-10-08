package authz

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Jishnu-Prasad888/Cairn/internal/audit"
	"github.com/Jishnu-Prasad888/Cairn/internal/likeutil"
)

// GrantInput describes an intended grant for creation.
type GrantInput struct {
	// UserID is the principal receiving the grant.
	UserID string
	// ResourceKey is the path-like key of the resource subtree.
	ResourceKey string
	// Caps are the capabilities to grant or deny.
	Caps CapSet
	// Effect is allow or deny.
	Effect Effect
	// CreatedBy is the acting user id for audit purposes.
	CreatedBy string
}

// Grant returns the persisted grant describing an authorization entry.
type Grant struct {
	ID           string
	UserID       string
	ResourceKey  string
	Capabilities CapSet
	Effect       Effect
	CreatedBy    string
	CreatedAt    time.Time
}

// CreateGrant inserts an allow or deny grant. A grant with the same
// (user, resource, effect) replaces any previous capabilities on that exact
// key; it is an upsert, so repeated calls are idempotent. Granting is itself an
// authorization decision made by the caller (the HTTP layer enforces manage).
func (s *Service) CreateGrant(ctx context.Context, in GrantInput) (*Grant, error) {
	if in.UserID == "" {
		return nil, &ValidationError{Field: "user_id", Reason: "must not be empty"}
	}
	if in.ResourceKey == "" {
		return nil, &ValidationError{Field: "resource_key", Reason: "must not be empty"}
	}
	if in.Effect != EffectAllow && in.Effect != EffectDeny {
		return nil, &ValidationError{Field: "effect", Reason: `must be "allow" or "deny"`}
	}
	if len(in.Caps) == 0 {
		return nil, &ValidationError{Field: "capabilities", Reason: "must not be empty"}
	}

	exists, err := s.UserExists(ctx, in.UserID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrUserNotFound
	}

	g := &Grant{
		ID:           newID(),
		UserID:       in.UserID,
		ResourceKey:  in.ResourceKey,
		Capabilities: cloneCaps(in.Caps),
		Effect:       in.Effect,
		CreatedBy:    in.CreatedBy,
		CreatedAt:    time.Now().UTC(),
	}

	const upsert = `
		INSERT INTO permission_grants (id, user_id, resource_key, capabilities, effect, created_by, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (user_id, resource_key, effect) DO UPDATE SET
			capabilities = excluded.capabilities,
			created_by   = excluded.created_by,
			created_at   = excluded.created_at`
	if _, err := s.db.ExecContext(ctx, upsert,
		g.ID, g.UserID, g.ResourceKey, capCSV(g.Capabilities), string(g.Effect),
		g.CreatedBy, rfc3339(g.CreatedAt)); err != nil {
		return nil, fmt.Errorf("insert grant: %w", err)
	}

	s.bumpGeneration()
	s.recordGrantEvent(ctx, g, "granted")
	return g, nil
}

// RevokeGrant removes a grant by id, freeing its capabilities back to
// inherited policy. Returns ErrNotFound when no such grant exists.
func (s *Service) RevokeGrant(ctx context.Context, grantID, actorID string) error {
	if grantID == "" {
		return &ValidationError{Field: "grant_id", Reason: "must not be empty"}
	}
	var (
		userID, resourceKey, effect string
		capsCSV                     string
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT user_id, resource_key, capabilities, effect FROM permission_grants WHERE id = ?`,
		grantID).Scan(&userID, &resourceKey, &capsCSV, &effect)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("lookup grant: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM permission_grants WHERE id = ?`, grantID); err != nil {
		return fmt.Errorf("delete grant: %w", err)
	}

	s.bumpGeneration()
	s.logger.Info("grant revoked", "grant_id", grantID, "user_id", userID, "resource_key", resourceKey)
	if s.audit != nil {
		s.audit.Record(ctx, audit.Event{
			ActorUserID: actorID,
			Action:      audit.ActionGrantRevoked,
			Details: map[string]any{
				"grant_id":     grantID,
				"user_id":      userID,
				"resource_key": resourceKey,
				"effect":       effect,
				"capabilities": capsCSV,
			},
		})
	}
	return nil
}

// ListGrants returns every grant that applies to resources with the given
// key prefix (usually the library key), newest last.
func (s *Service) ListGrants(ctx context.Context, keyPrefix string) ([]Grant, error) {
	prefix := keyPrefix
	if prefix != "" {
		prefix += keySep
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, resource_key, capabilities, effect, created_by, created_at
		FROM permission_grants
		WHERE resource_key = ? OR resource_key LIKE ? `+likeutil.EscapeClause+`
		ORDER BY created_at`, keyPrefix, likeutil.Escape(prefix)+"%")
	if err != nil {
		return nil, fmt.Errorf("list grants: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Grant
	for rows.Next() {
		g, err := scanGrant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate grants: %w", err)
	}
	return out, nil
}

// GrantByID returns a single grant by id, or ErrNotFound.
func (s *Service) GrantByID(ctx context.Context, grantID string) (*Grant, error) {
	return scanGrant(s.db.QueryRowContext(ctx, `
		SELECT id, user_id, resource_key, capabilities, effect, created_by, created_at
		FROM permission_grants WHERE id = ?`, grantID))
}

// scanGrant scans one grant row.
func scanGrant(row rowScanner) (*Grant, error) {
	var (
		g       Grant
		capsCSV string
		effect  string
		created string
	)
	if err := row.Scan(&g.ID, &g.UserID, &g.ResourceKey, &capsCSV, &effect, &g.CreatedBy, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scan grant: %w", err)
	}
	set, err := capSetFromStrings(strings.Split(capsCSV, ","))
	if err != nil {
		return nil, fmt.Errorf("corrupt capabilities %q: %w", capsCSV, err)
	}
	g.Capabilities = set
	g.Effect = Effect(effect)
	if g.CreatedAt, err = parseTime(created); err != nil {
		return nil, err
	}
	return &g, nil
}

// recordGrantEvent writes an audit event for a grant change, best-effort.
func (s *Service) recordGrantEvent(ctx context.Context, g *Grant, _ string) {
	if s.audit == nil {
		return
	}
	s.audit.Record(ctx, audit.Event{
		ActorUserID:  g.CreatedBy,
		TargetUserID: g.UserID,
		Action:       audit.ActionGrantCreated,
		Details: map[string]any{
			"resource_key": g.ResourceKey,
			"effect":       string(g.Effect),
			"capabilities": strings.Join(capStrings(g.Capabilities), ","),
		},
	})
}
