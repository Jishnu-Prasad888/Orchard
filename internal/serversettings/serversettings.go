// Package serversettings reads and writes server-wide switches that an
// administrator changes at runtime, stored in the server database's
// server_settings table (one row per key, JSON-encoded value).
package serversettings

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Store is the repository for server-wide settings.
type Store struct {
	db *sql.DB
}

// NewStore wraps the server database pool.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// GetBool returns the stored boolean for key, or def when no row exists.
func (s *Store) GetBool(ctx context.Context, key string, def bool) (bool, error) {
	var value string
	err := s.db.QueryRowContext(ctx,
		`SELECT value FROM server_settings WHERE key = ?`, key).Scan(&value)
	if err != nil {
		if err == sql.ErrNoRows {
			return def, nil
		}
		return false, fmt.Errorf("get server setting %s: %w", key, err)
	}
	return value == "true", nil
}

// SetBool stores a boolean for key.
func (s *Store) SetBool(ctx context.Context, key string, value bool) error {
	v := "false"
	if value {
		v = "true"
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO server_settings (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, v, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("set server setting %s: %w", key, err)
	}
	return nil
}
