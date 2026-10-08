// Package favorites manages the user's starred/favorite files in a per-library
// SQLite database.
package favorites

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/Jishnu-Prasad888/Cairn/internal/media"
)

// ErrAlreadyFavorited is returned when trying to favorite a file that is already favorited.
var ErrAlreadyFavorited = errors.New("file is already favorited")

// ErrNotFavorited is returned when trying to unfavorite a file that is not favorited.
var ErrNotFavorited = errors.New("file is not favorited")

// FavoriteStore is the repository for favorites in a per-library database.
type FavoriteStore struct {
	db        *sql.DB
	libraryID string
}

// NewFavoriteStore wraps a per-library database pool.
func NewFavoriteStore(db *sql.DB, libraryID string) *FavoriteStore {
	return &FavoriteStore{db: db, libraryID: libraryID}
}

// Add marks a file as a favorite.
func (s *FavoriteStore) Add(ctx context.Context, fileID string) error {
	now := rfc3339(time.Now().UTC())
	res, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO favorites (file_id, created_at) VALUES (?, ?)`,
		fileID, now)
	if err != nil {
		return fmt.Errorf("add favorite: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrAlreadyFavorited
	}
	return nil
}

// Remove removes a file from favorites.
func (s *FavoriteStore) Remove(ctx context.Context, fileID string) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM favorites WHERE file_id = ?`, fileID)
	if err != nil {
		return fmt.Errorf("remove favorite: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFavorited
	}
	return nil
}

// IsFavorite returns true if the file is favorited.
func (s *FavoriteStore) IsFavorite(ctx context.Context, fileID string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM favorites WHERE file_id = ?`, fileID).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("check favorite: %w", err)
	}
	return n > 0, nil
}

// ListFiles returns all favorited files ordered by when they were favorited (newest first).
func (s *FavoriteStore) ListFiles(ctx context.Context) ([]*media.File, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT f.id, f.rel_path, f.size_bytes, f.mod_time, f.content_hash,
		        f.status, f.first_seen_at, f.last_seen_at
		 FROM indexed_files f
		 JOIN favorites fav ON fav.file_id = f.id
		 WHERE f.status = 'present'
		 ORDER BY fav.created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list favorites: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return s.scanFiles(rows)
}

// --- row scanner ---

func (s *FavoriteStore) scanFiles(rows *sql.Rows) ([]*media.File, error) {
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
		err := rows.Scan(&f.ID, &f.RelPath, &f.SizeBytes, &modStr, &hash, &status, &firstStr, &lastStr)
		if err != nil {
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

// --- helpers ---

func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func parseTime(s string, out *time.Time) error {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return fmt.Errorf("parse time %q: %w", s, err)
	}
	*out = t
	return nil
}
