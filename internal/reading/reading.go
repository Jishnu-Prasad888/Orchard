// Package reading tracks a user's reading status for a file (want to read,
// reading, completed, on hold) in a per-library SQLite database.
package reading

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Status is a user's progress through a book.
type Status string

const (
	StatusWantToRead Status = "want_to_read"
	StatusReading    Status = "reading"
	StatusCompleted  Status = "completed"
	StatusOnHold     Status = "on_hold"
)

// Valid reports whether s is one of the known statuses.
func (s Status) Valid() bool {
	switch s {
	case StatusWantToRead, StatusReading, StatusCompleted, StatusOnHold:
		return true
	default:
		return false
	}
}

// Store is the repository for per-user reading status in a per-library database.
type Store struct {
	db *sql.DB
}

// NewStore wraps a per-library database pool.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Get returns the given user's reading status for a file. ok is false when
// no status has been set (not the same as any particular Status value).
func (s *Store) Get(ctx context.Context, userID, fileID string) (Status, bool, error) {
	var status string
	err := s.db.QueryRowContext(ctx,
		`SELECT status FROM reading_status WHERE user_id = ? AND file_id = ?`, userID, fileID).
		Scan(&status)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", false, nil
		}
		return "", false, fmt.Errorf("get reading status: %w", err)
	}
	return Status(status), true, nil
}

// Set upserts the given user's reading status for a file.
func (s *Store) Set(ctx context.Context, userID, fileID string, status Status) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO reading_status (user_id, file_id, status, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(user_id, file_id) DO UPDATE SET
			status = excluded.status,
			updated_at = excluded.updated_at`,
		userID, fileID, string(status), time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("set reading status: %w", err)
	}
	return nil
}

// Clear removes the given user's reading status for a file. Clearing a
// status that was never set is not an error.
func (s *Store) Clear(ctx context.Context, userID, fileID string) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM reading_status WHERE user_id = ? AND file_id = ?`, userID, fileID); err != nil {
		return fmt.Errorf("clear reading status: %w", err)
	}
	return nil
}
