// Package usersettings reads and writes per-user preferences stored on the
// server, in the server database's user_settings table (one row per
// user+key, JSON-encoded value). A preference follows the user across
// browsers and future native clients.
package usersettings

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// Store is the repository for per-user settings.
type Store struct {
	db *sql.DB
}

// NewStore wraps the server database pool.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// GetJSON unmarshals the stored value for userID+key into dest. ok is
// false when no row exists (dest is left untouched).
func (s *Store) GetJSON(ctx context.Context, userID, key string, dest any) (bool, error) {
	var value string
	err := s.db.QueryRowContext(ctx,
		`SELECT value FROM user_settings WHERE user_id = ? AND key = ?`, userID, key).Scan(&value)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, fmt.Errorf("get user setting %s: %w", key, err)
	}
	if err := json.Unmarshal([]byte(value), dest); err != nil {
		return false, fmt.Errorf("decode user setting %s: %w", key, err)
	}
	return true, nil
}

// SetJSON marshals value and stores it for userID+key.
func (s *Store) SetJSON(ctx context.Context, userID, key string, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode user setting %s: %w", key, err)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO user_settings (user_id, key, value, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(user_id, key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		userID, key, string(encoded), time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("set user setting %s: %w", key, err)
	}
	return nil
}
