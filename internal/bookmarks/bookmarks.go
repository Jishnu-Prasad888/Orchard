// Package bookmarks manages per-user bookmarks into files in a per-library
// SQLite database. A bookmark marks a specific place in a file (an opaque
// reader location, following the same convention as internal/progress) that
// a user wants to return to later. Unlike reading progress, which is a
// single resume point per (user, file), a user may have many bookmarks in
// the same file, so each bookmark gets its own id.
package bookmarks

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// ErrNotFound is returned when a bookmark does not exist, or exists but is
// owned by a different user (the two are indistinguishable to the caller,
// so another user's bookmark never leaks its existence).
var ErrNotFound = errors.New("bookmark not found")

// Bookmark marks a place in a file a user wants to return to.
type Bookmark struct {
	ID        string
	UserID    string
	FileID    string
	Location  string
	Label     string
	CreatedAt time.Time
}

// Store is the repository for bookmarks in a per-library database.
type Store struct {
	db *sql.DB
}

// NewStore wraps a per-library database pool.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Create adds a bookmark at location in fileID, owned by userID.
func (s *Store) Create(ctx context.Context, userID, fileID, location, label string) (*Bookmark, error) {
	if location == "" {
		return nil, fmt.Errorf("bookmark location must not be empty")
	}
	id := newID()
	now := rfc3339(time.Now().UTC())
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO bookmarks (id, user_id, file_id, location, label, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		id, userID, fileID, location, label, now)
	if err != nil {
		return nil, fmt.Errorf("create bookmark: %w", err)
	}
	return s.GetOwned(ctx, id, userID)
}

// GetOwned returns a bookmark by ID, scoped to its owner. Returns
// ErrNotFound both when the ID does not exist and when it belongs to a
// different user.
func (s *Store) GetOwned(ctx context.Context, id, userID string) (*Bookmark, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, user_id, file_id, location, label, created_at
		FROM bookmarks
		WHERE id = ? AND user_id = ?`, id, userID)
	return scanBookmark(row)
}

// ListByFile returns userID's bookmarks in fileID, oldest first.
func (s *Store) ListByFile(ctx context.Context, userID, fileID string) ([]*Bookmark, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, file_id, location, label, created_at
		FROM bookmarks
		WHERE user_id = ? AND file_id = ?
		ORDER BY created_at ASC`, userID, fileID)
	if err != nil {
		return nil, fmt.Errorf("list bookmarks by file: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanBookmarks(rows)
}

// List returns all of userID's bookmarks across the library, newest first.
func (s *Store) List(ctx context.Context, userID string) ([]*Bookmark, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, file_id, location, label, created_at
		FROM bookmarks
		WHERE user_id = ?
		ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list bookmarks: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanBookmarks(rows)
}

// UpdateLabel renames a bookmark owned by userID.
func (s *Store) UpdateLabel(ctx context.Context, id, userID, label string) (*Bookmark, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE bookmarks SET label = ? WHERE id = ? AND user_id = ?`, label, id, userID)
	if err != nil {
		return nil, fmt.Errorf("update bookmark: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, ErrNotFound
	}
	return s.GetOwned(ctx, id, userID)
}

// Delete removes a bookmark owned by userID. Returns ErrNotFound if id does
// not exist or is not owned by userID.
func (s *Store) Delete(ctx context.Context, id, userID string) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM bookmarks WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return fmt.Errorf("delete bookmark: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// --- row scanners ---

type rowScanner interface {
	Scan(dest ...any) error
}

func scanBookmark(row rowScanner) (*Bookmark, error) {
	var (
		b          Bookmark
		createdStr string
	)
	err := row.Scan(&b.ID, &b.UserID, &b.FileID, &b.Location, &b.Label, &createdStr)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan bookmark: %w", err)
	}
	if err := parseTime(createdStr, &b.CreatedAt); err != nil {
		return nil, err
	}
	return &b, nil
}

func scanBookmarks(rows *sql.Rows) ([]*Bookmark, error) {
	var out []*Bookmark
	for rows.Next() {
		b, err := scanBookmark(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// --- helpers ---

func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("fallback-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func parseTime(s string, out *time.Time) error {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return fmt.Errorf("parse time %q: %w", s, err)
	}
	*out = t
	return nil
}
