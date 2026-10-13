// Package quotes manages per-user saved quotes from files in a per-library
// SQLite database. A quote is a passage of text a user has saved together
// with their own commentary and citation details (author/chapter/page),
// independent of any highlight — it can be typed in by hand rather than
// selected in a reader. location is optional and, when present, is an
// opaque reader location (following the same convention as
// internal/bookmarks and internal/highlights) that lets "jump to quote"
// reuse the reader navigation hook.
package quotes

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// ErrNotFound is returned when a quote does not exist, or exists but is
// owned by a different user (the two are indistinguishable to the caller,
// so another user's quote never leaks its existence).
var ErrNotFound = errors.New("quote not found")

// Quote is a saved passage of text with citation details and commentary.
type Quote struct {
	ID         string
	UserID     string
	FileID     string
	Text       string
	Location   string
	Author     string
	Chapter    string
	Page       string
	Commentary string
	Favorite   bool
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Fields describes the editable fields of a quote.
type Fields struct {
	Text       string
	Location   string
	Author     string
	Chapter    string
	Page       string
	Commentary string
}

// Store is the repository for quotes in a per-library database.
type Store struct {
	db *sql.DB
}

// NewStore wraps a per-library database pool.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Create adds a quote sourced from fileID, owned by userID.
func (s *Store) Create(ctx context.Context, userID, fileID string, f Fields) (*Quote, error) {
	if f.Text == "" {
		return nil, fmt.Errorf("quote text must not be empty")
	}
	id := newID()
	now := rfc3339(time.Now().UTC())
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO quotes (id, user_id, file_id, text, location, author, chapter, page, commentary, favorite, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?)`,
		id, userID, fileID, f.Text, f.Location, f.Author, f.Chapter, f.Page, f.Commentary, now, now)
	if err != nil {
		return nil, fmt.Errorf("create quote: %w", err)
	}
	return s.GetOwned(ctx, id, userID)
}

// GetOwned returns a quote by ID, scoped to its owner. Returns ErrNotFound
// both when the ID does not exist and when it belongs to a different user.
func (s *Store) GetOwned(ctx context.Context, id, userID string) (*Quote, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, user_id, file_id, text, location, author, chapter, page, commentary, favorite, created_at, updated_at
		FROM quotes
		WHERE id = ? AND user_id = ?`, id, userID)
	return scanQuote(row)
}

// ListOptions filters a quote listing.
type ListOptions struct {
	FileID       string // non-empty: only quotes sourced from this file
	FavoriteOnly bool
}

// List returns userID's quotes across the library, newest first, optionally
// filtered by file or favorite status.
func (s *Store) List(ctx context.Context, userID string, opts ListOptions) ([]*Quote, error) {
	query := `
		SELECT id, user_id, file_id, text, location, author, chapter, page, commentary, favorite, created_at, updated_at
		FROM quotes
		WHERE user_id = ?`
	args := []any{userID}
	if opts.FileID != "" {
		query += ` AND file_id = ?`
		args = append(args, opts.FileID)
	}
	if opts.FavoriteOnly {
		query += ` AND favorite = 1`
	}
	query += ` ORDER BY created_at DESC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list quotes: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanQuotes(rows)
}

// Update describes a patch to a quote. Only non-nil fields are changed.
type Update struct {
	Text       *string
	Author     *string
	Chapter    *string
	Page       *string
	Commentary *string
	Favorite   *bool
}

// Update applies a patch to a quote owned by userID.
func (s *Store) Update(ctx context.Context, id, userID string, patch Update) (*Quote, error) {
	existing, err := s.GetOwned(ctx, id, userID)
	if err != nil {
		return nil, err
	}
	text := existing.Text
	if patch.Text != nil {
		text = *patch.Text
	}
	if text == "" {
		return nil, fmt.Errorf("quote text must not be empty")
	}
	author := existing.Author
	if patch.Author != nil {
		author = *patch.Author
	}
	chapter := existing.Chapter
	if patch.Chapter != nil {
		chapter = *patch.Chapter
	}
	page := existing.Page
	if patch.Page != nil {
		page = *patch.Page
	}
	commentary := existing.Commentary
	if patch.Commentary != nil {
		commentary = *patch.Commentary
	}
	favorite := existing.Favorite
	if patch.Favorite != nil {
		favorite = *patch.Favorite
	}
	now := rfc3339(time.Now().UTC())
	_, err = s.db.ExecContext(ctx,
		`UPDATE quotes SET text = ?, author = ?, chapter = ?, page = ?, commentary = ?, favorite = ?, updated_at = ?
		 WHERE id = ? AND user_id = ?`,
		text, author, chapter, page, commentary, boolToInt(favorite), now, id, userID)
	if err != nil {
		return nil, fmt.Errorf("update quote: %w", err)
	}
	return s.GetOwned(ctx, id, userID)
}

// Delete removes a quote owned by userID. Returns ErrNotFound if id does
// not exist or is not owned by userID.
func (s *Store) Delete(ctx context.Context, id, userID string) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM quotes WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return fmt.Errorf("delete quote: %w", err)
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

func scanQuote(row rowScanner) (*Quote, error) {
	var (
		q                      Quote
		favorite               int
		createdStr, updatedStr string
	)
	err := row.Scan(&q.ID, &q.UserID, &q.FileID, &q.Text, &q.Location, &q.Author, &q.Chapter,
		&q.Page, &q.Commentary, &favorite, &createdStr, &updatedStr)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan quote: %w", err)
	}
	q.Favorite = favorite != 0
	if err := parseTime(createdStr, &q.CreatedAt); err != nil {
		return nil, err
	}
	if err := parseTime(updatedStr, &q.UpdatedAt); err != nil {
		return nil, err
	}
	return &q, nil
}

func scanQuotes(rows *sql.Rows) ([]*Quote, error) {
	var out []*Quote
	for rows.Next() {
		q, err := scanQuote(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, q)
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

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
