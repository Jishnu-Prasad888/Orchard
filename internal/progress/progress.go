// Package progress tracks a user's resume point in a book, in a per-library
// SQLite database.
package progress

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"time"

	"github.com/Jishnu-Prasad888/Orchard/internal/media"
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

// InProgress is a file the given user has started but not finished, for
// "continue reading" surfaces.
type InProgress struct {
	File *media.File
	Progress
}

// ListInProgress returns the given user's most recently updated in-progress
// books (0 < percent < 100), newest first, up to limit.
func (s *Store) ListInProgress(ctx context.Context, userID, libraryID string, limit int) ([]InProgress, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT f.id, f.rel_path, f.size_bytes, f.mod_time, f.content_hash,
		       f.status, f.first_seen_at, f.last_seen_at,
		       p.location, p.percent, p.updated_at
		FROM indexed_files f
		JOIN reading_progress p ON p.file_id = f.id
		WHERE f.status = 'present' AND p.user_id = ? AND p.percent > 0 AND p.percent < 100
		ORDER BY p.updated_at DESC
		LIMIT ?`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("list in-progress files: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []InProgress
	for rows.Next() {
		var (
			f        media.File
			modStr   string
			firstStr string
			lastStr  string
			hash     sql.NullString
			status   string
			ip       InProgress
			updStr   string
		)
		err := rows.Scan(&f.ID, &f.RelPath, &f.SizeBytes, &modStr, &hash, &status, &firstStr, &lastStr,
			&ip.Location, &ip.Percent, &updStr)
		if err != nil {
			return nil, fmt.Errorf("scan in-progress file: %w", err)
		}
		f.LibraryID = libraryID
		f.ContentHash = hash.String
		f.Status = media.FileStatus(status)
		f.Name = filepath.Base(f.RelPath)
		f.FolderPath = filepath.ToSlash(filepath.Dir(f.RelPath))
		if f.FolderPath == "." {
			f.FolderPath = ""
		}
		f.MediaType = media.DetectMediaType(f.RelPath)
		f.MIMEType = media.DetectMIME(f.RelPath)
		if err := parseTime(modStr, &f.ModTime); err != nil {
			return nil, err
		}
		if err := parseTime(firstStr, &f.FirstSeenAt); err != nil {
			return nil, err
		}
		if err := parseTime(lastStr, &f.LastSeenAt); err != nil {
			return nil, err
		}
		if err := parseTime(updStr, &ip.UpdatedAt); err != nil {
			return nil, err
		}
		ip.FileID = f.ID
		ip.File = &f
		out = append(out, ip)
	}
	return out, rows.Err()
}

func parseTime(s string, out *time.Time) error {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return fmt.Errorf("parse time %q: %w", s, err)
	}
	*out = t
	return nil
}
