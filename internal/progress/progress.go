// Package progress tracks a user's resume point in a book, in a per-library
// SQLite database.
package progress

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Progress is a user's reading position in a file.
type Progress struct {
	FileID string
	// Location is opaque to the server: a page number for PDF, a CFI
	// string for EPUB, a scroll fraction for Markdown/text — meaningful
	// only to the reader that wrote it.
	Location string
	// Percent is reader-agnostic (0-100), for drawing a progress bar
	// without understanding Location's format.
	Percent   float64
	UpdatedAt time.Time
}

// Store is the repository for per-user reading progress in a per-library database.
type Store struct {
	db *sql.DB
}

// NewStore wraps a per-library database pool.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Get returns the given user's reading progress for a file. ok is false
// when no progress has been recorded yet.
func (s *Store) Get(ctx context.Context, userID, fileID string) (Progress, bool, error) {
	var (
		p   Progress
		upd string
	)
	p.FileID = fileID
	err := s.db.QueryRowContext(ctx,
		`SELECT location, percent, updated_at FROM reading_progress WHERE user_id = ? AND file_id = ?`,
		userID, fileID).
		Scan(&p.Location, &p.Percent, &upd)
	if err != nil {
		if err == sql.ErrNoRows {
			return Progress{}, false, nil
		}
		return Progress{}, false, fmt.Errorf("get reading progress: %w", err)
	}
	if t, perr := time.Parse(time.RFC3339Nano, upd); perr == nil {
		p.UpdatedAt = t
	}
	return p, true, nil
}

// Set upserts the given user's reading position for a file.
func (s *Store) Set(ctx context.Context, userID, fileID, location string, percent float64) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO reading_progress (user_id, file_id, location, percent, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(user_id, file_id) DO UPDATE SET
			location   = excluded.location,
			percent    = excluded.percent,
			updated_at = excluded.updated_at`,
		userID, fileID, location, percent, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("set reading progress: %w", err)
	}
	return nil
}

// Clear removes the given user's reading progress for a file. Clearing
// progress that was never set is not an error.
func (s *Store) Clear(ctx context.Context, userID, fileID string) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM reading_progress WHERE user_id = ? AND file_id = ?`, userID, fileID); err != nil {
		return fmt.Errorf("clear reading progress: %w", err)
	}
	return nil
}
