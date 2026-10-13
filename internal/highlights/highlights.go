// Package highlights manages per-user text highlights into files in a
// per-library SQLite database. A highlight captures a passage of text a
// user selected in a reader, together with an optional note and a color,
// at an opaque reader location (following the same convention as
// internal/progress and internal/bookmarks).
package highlights

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// ErrNotFound is returned when a highlight does not exist, or exists but is
// owned by a different user (the two are indistinguishable to the caller,
// so another user's highlight never leaks its existence).
var ErrNotFound = errors.New("highlight not found")

// DefaultColor is used when a highlight is created without an explicit
// color.
const DefaultColor = "yellow"

// Highlight is a user-selected passage of text at a location in a file.
type Highlight struct {
	ID        string
	UserID    string
	FileID    string
	Location  string
	Text      string
	Color     string
	Note      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Store is the repository for highlights in a per-library database.
type Store struct {
	db *sql.DB
}

// NewStore wraps a per-library database pool.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Create adds a highlight at location in fileID, owned by userID. color
// defaults to DefaultColor if empty.
func (s *Store) Create(ctx context.Context, userID, fileID, location, text, color, note string) (*Highlight, error) {
	if location == "" {
		return nil, fmt.Errorf("highlight location must not be empty")
	}
	if color == "" {
		color = DefaultColor
	}
	id := newID()
	now := rfc3339(time.Now().UTC())
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO highlights (id, user_id, file_id, location, text, color, note, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, userID, fileID, location, text, color, note, now, now)
	if err != nil {
		return nil, fmt.Errorf("create highlight: %w", err)
	}
	return s.GetOwned(ctx, id, userID)
}

// GetOwned returns a highlight by ID, scoped to its owner. Returns
// ErrNotFound both when the ID does not exist and when it belongs to a
// different user.
func (s *Store) GetOwned(ctx context.Context, id, userID string) (*Highlight, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, user_id, file_id, location, text, color, note, created_at, updated_at
		FROM highlights
		WHERE id = ? AND user_id = ?`, id, userID)
	return scanHighlight(row)
}

// ListByFile returns userID's highlights in fileID, oldest first.
func (s *Store) ListByFile(ctx context.Context, userID, fileID string) ([]*Highlight, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, file_id, location, text, color, note, created_at, updated_at
		FROM highlights
		WHERE user_id = ? AND file_id = ?
		ORDER BY created_at ASC`, userID, fileID)
	if err != nil {
		return nil, fmt.Errorf("list highlights by file: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanHighlights(rows)
}

// List returns all of userID's highlights across the library, newest first.
func (s *Store) List(ctx context.Context, userID string) ([]*Highlight, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, file_id, location, text, color, note, created_at, updated_at
		FROM highlights
		WHERE user_id = ?
		ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list highlights: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanHighlights(rows)
}

// Update describes a patch to a highlight. Only non-nil fields are changed.
type Update struct {
	Color *string
	Note  *string
}

// Update applies a patch to a highlight owned by userID.
func (s *Store) Update(ctx context.Context, id, userID string, patch Update) (*Highlight, error) {
	existing, err := s.GetOwned(ctx, id, userID)
	if err != nil {
		return nil, err
	}
	color := existing.Color
	if patch.Color != nil && *patch.Color != "" {
		color = *patch.Color
	}
	note := existing.Note
	if patch.Note != nil {
		note = *patch.Note
	}
	now := rfc3339(time.Now().UTC())
	_, err = s.db.ExecContext(ctx,
		`UPDATE highlights SET color = ?, note = ?, updated_at = ? WHERE id = ? AND user_id = ?`,
		color, note, now, id, userID)
	if err != nil {
		return nil, fmt.Errorf("update highlight: %w", err)
	}
	return s.GetOwned(ctx, id, userID)
}

// Delete removes a highlight owned by userID. Returns ErrNotFound if id
// does not exist or is not owned by userID.
func (s *Store) Delete(ctx context.Context, id, userID string) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM highlights WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return fmt.Errorf("delete highlight: %w", err)
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

func scanHighlight(row rowScanner) (*Highlight, error) {
	var (
		h                      Highlight
		createdStr, updatedStr string
	)
	err := row.Scan(&h.ID, &h.UserID, &h.FileID, &h.Location, &h.Text, &h.Color, &h.Note,
		&createdStr, &updatedStr)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan highlight: %w", err)
	}
	if err := parseTime(createdStr, &h.CreatedAt); err != nil {
		return nil, err
	}
	if err := parseTime(updatedStr, &h.UpdatedAt); err != nil {
		return nil, err
	}
	return &h, nil
}

func scanHighlights(rows *sql.Rows) ([]*Highlight, error) {
	var out []*Highlight
	for rows.Next() {
		h, err := scanHighlight(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
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
