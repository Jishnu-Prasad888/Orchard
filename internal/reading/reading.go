// Package reading tracks a user's reading status for a file (want to read,
// reading, completed, on hold) in a per-library SQLite database.
package reading

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
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

// CompletedAmong returns which of fileIDs the given user has marked
// completed, in one query — a listing calls it once for its whole page.
func (s *Store) CompletedAmong(ctx context.Context, userID string, fileIDs []string) (map[string]bool, error) {
	out := make(map[string]bool)
	if len(fileIDs) == 0 {
		return out, nil
	}
	placeholders := strings.Repeat("?,", len(fileIDs))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, 0, len(fileIDs)+2)
	args = append(args, userID, string(StatusCompleted))
	for _, id := range fileIDs {
		args = append(args, id)
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT file_id FROM reading_status
		 WHERE user_id = ? AND status = ? AND file_id IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("list completed files: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan completed file: %w", err)
		}
		out[id] = true
	}
	return out, rows.Err()
}
