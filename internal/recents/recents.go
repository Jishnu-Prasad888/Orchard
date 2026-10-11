// Package recents tracks, per user, the last time each file was opened in
// the viewer, in a per-library SQLite database. It is "last opened" rather
// than a full history log: one row per (user, file), timestamp overwritten
// on every open, so "recently opened" is a single indexed query.
package recents

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"time"

	"github.com/Jishnu-Prasad888/Orchard/internal/media"
)

// Store is the repository for per-user file-open tracking in a per-library database.
type Store struct {
	db        *sql.DB
	libraryID string
}

// NewStore wraps a per-library database pool.
func NewStore(db *sql.DB, libraryID string) *Store {
	return &Store{db: db, libraryID: libraryID}
}

// MarkOpened records that the given user opened a file just now.
func (s *Store) MarkOpened(ctx context.Context, userID, fileID string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO file_opens (user_id, file_id, opened_at) VALUES (?, ?, ?)
		ON CONFLICT(user_id, file_id) DO UPDATE SET opened_at = excluded.opened_at`,
		userID, fileID, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("mark file opened: %w", err)
	}
	return nil
}

// ListRecentlyOpened returns the given user's most recently opened files,
// newest first, capped at limit. Only files still present are included,
// matching the favorites list's behavior.
func (s *Store) ListRecentlyOpened(ctx context.Context, userID string, limit int) ([]*media.File, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT f.id, f.rel_path, f.size_bytes, f.mod_time, f.content_hash,
		        f.status, f.first_seen_at, f.last_seen_at
		 FROM indexed_files f
		 JOIN file_opens o ON o.file_id = f.id
		 WHERE f.status = 'present' AND o.user_id = ?
		 ORDER BY o.opened_at DESC
		 LIMIT ?`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("list recently opened: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return s.scanFiles(rows)
}

func (s *Store) scanFiles(rows *sql.Rows) ([]*media.File, error) {
	var out []*media.File
	for rows.Next() {
		var (
			f        media.File
			modStr   string
			firstStr string
			lastStr  string
			hash     sql.NullString
			status   string
		)
		if err := rows.Scan(&f.ID, &f.RelPath, &f.SizeBytes, &modStr, &hash, &status, &firstStr, &lastStr); err != nil {
			return nil, fmt.Errorf("scan file: %w", err)
		}
		f.LibraryID = s.libraryID
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
		out = append(out, &f)
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
