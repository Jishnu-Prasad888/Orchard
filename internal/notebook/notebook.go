// Package notebook manages long-form, per-user Markdown notes in a
// per-library SQLite database: the "Notes" feature (many notes per file, or
// a standalone note with no file at all), as distinct from internal/notes,
// which remains the lightweight single-caption-per-file editor in the
// viewer. A note may optionally be about a file, a saved quote, or a
// highlight — all three links are independent and optional.
package notebook

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// ErrNotFound is returned when a note does not exist, or exists but is
// owned by a different user (the two are indistinguishable to the caller,
// so another user's note never leaks its existence).
var ErrNotFound = errors.New("note not found")

// Note is a long-form, per-user piece of Markdown writing.
type Note struct {
	ID          string
	UserID      string
	FileID      string // "" when not about a file
	QuoteID     string // "" when not about a quote
	HighlightID string // "" when not about a highlight
	Title       string
	Body        string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Fields describes the editable/creatable content of a note.
type Fields struct {
	Title       string
	Body        string
	FileID      string
	QuoteID     string
	HighlightID string
}

// Store is the repository for notes in a per-library database.
type Store struct {
	db *sql.DB
}

// NewStore wraps a per-library database pool.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Create adds a note owned by userID.
func (s *Store) Create(ctx context.Context, userID string, f Fields) (*Note, error) {
	id := newID()
	now := rfc3339(time.Now().UTC())
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO notes (id, user_id, file_id, quote_id, highlight_id, title, body, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, userID, nullable(f.FileID), nullable(f.QuoteID), nullable(f.HighlightID), f.Title, f.Body, now, now)
	if err != nil {
		return nil, fmt.Errorf("create note: %w", err)
	}
	return s.GetOwned(ctx, id, userID)
}

// GetOwned returns a note by ID, scoped to its owner. Returns ErrNotFound
// both when the ID does not exist and when it belongs to a different user.
func (s *Store) GetOwned(ctx context.Context, id, userID string) (*Note, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, user_id, file_id, quote_id, highlight_id, title, body, created_at, updated_at
		FROM notes
		WHERE id = ? AND user_id = ?`, id, userID)
	return scanNote(row)
}

// ListOptions filters a note listing. Only one of FileID/QuoteID/
// HighlightID/Standalone is expected to be set at a time.
type ListOptions struct {
	FileID      string
	QuoteID     string
	HighlightID string
	TagID       string
	// Standalone restricts the listing to notes with no file, quote, or
	// highlight link.
	Standalone bool
}

// List returns userID's notes across the library, newest first, optionally
// filtered.
func (s *Store) List(ctx context.Context, userID string, opts ListOptions) ([]*Note, error) {
	query := `
		SELECT n.id, n.user_id, n.file_id, n.quote_id, n.highlight_id, n.title, n.body, n.created_at, n.updated_at
		FROM notes n`
	args := []any{userID}
	where := `WHERE n.user_id = ?`
	if opts.TagID != "" {
		query += ` JOIN note_tags nt ON nt.note_id = n.id`
		where += ` AND nt.tag_id = ?`
		args = append(args, opts.TagID)
	}
	switch {
	case opts.FileID != "":
		where += ` AND n.file_id = ?`
		args = append(args, opts.FileID)
	case opts.QuoteID != "":
		where += ` AND n.quote_id = ?`
		args = append(args, opts.QuoteID)
	case opts.HighlightID != "":
		where += ` AND n.highlight_id = ?`
		args = append(args, opts.HighlightID)
	case opts.Standalone:
		where += ` AND n.file_id IS NULL AND n.quote_id IS NULL AND n.highlight_id IS NULL`
	}
	rows, err := s.db.QueryContext(ctx, query+" "+where+" ORDER BY n.updated_at DESC", args...)
	if err != nil {
		return nil, fmt.Errorf("list notes: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanNotes(rows)
}

// Update describes a patch to a note. Only non-nil fields are changed; the
// file/quote/highlight links are fixed at creation and not editable here.
type Update struct {
	Title *string
	Body  *string
}

// Update applies a patch to a note owned by userID.
func (s *Store) Update(ctx context.Context, id, userID string, patch Update) (*Note, error) {
	existing, err := s.GetOwned(ctx, id, userID)
	if err != nil {
		return nil, err
	}
	title := existing.Title
	if patch.Title != nil {
		title = *patch.Title
	}
	body := existing.Body
	if patch.Body != nil {
		body = *patch.Body
	}
	now := rfc3339(time.Now().UTC())
	_, err = s.db.ExecContext(ctx,
		`UPDATE notes SET title = ?, body = ?, updated_at = ? WHERE id = ? AND user_id = ?`,
		title, body, now, id, userID)
	if err != nil {
		return nil, fmt.Errorf("update note: %w", err)
	}
	return s.GetOwned(ctx, id, userID)
}

// Delete removes a note owned by userID. Returns ErrNotFound if id does
// not exist or is not owned by userID.
func (s *Store) Delete(ctx context.Context, id, userID string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM notes WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return fmt.Errorf("delete note: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Link creates a directed link from note id to linked note linkedID, both
// owned by userID. No-op if the link already exists.
func (s *Store) Link(ctx context.Context, userID, id, linkedID string) error {
	if _, err := s.GetOwned(ctx, id, userID); err != nil {
		return err
	}
	if _, err := s.GetOwned(ctx, linkedID, userID); err != nil {
		return err
	}
	now := rfc3339(time.Now().UTC())
	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO note_links (note_id, linked_note_id, created_at) VALUES (?, ?, ?)`,
		id, linkedID, now)
	if err != nil {
		return fmt.Errorf("link note: %w", err)
	}
	return nil
}

// Unlink removes a link between two notes owned by userID, in either
// direction it was originally created.
func (s *Store) Unlink(ctx context.Context, userID, id, linkedID string) error {
	if _, err := s.GetOwned(ctx, id, userID); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM note_links
		 WHERE (note_id = ? AND linked_note_id = ?) OR (note_id = ? AND linked_note_id = ?)`,
		id, linkedID, linkedID, id)
	if err != nil {
		return fmt.Errorf("unlink note: %w", err)
	}
	return nil
}

// ListLinked returns the notes linked to id (in either direction) that are
// owned by userID.
func (s *Store) ListLinked(ctx context.Context, userID, id string) ([]*Note, error) {
	if _, err := s.GetOwned(ctx, id, userID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT n.id, n.user_id, n.file_id, n.quote_id, n.highlight_id, n.title, n.body, n.created_at, n.updated_at
		FROM notes n
		WHERE n.user_id = ? AND n.id IN (
			SELECT linked_note_id FROM note_links WHERE note_id = ?
			UNION
			SELECT note_id FROM note_links WHERE linked_note_id = ?
		)
		ORDER BY n.updated_at DESC`, userID, id, id)
	if err != nil {
		return nil, fmt.Errorf("list linked notes: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanNotes(rows)
}

// --- row scanners ---

type rowScanner interface {
	Scan(dest ...any) error
}

func scanNote(row rowScanner) (*Note, error) {
	var (
		n                            Note
		fileID, quoteID, highlightID sql.NullString
		createdStr, updatedStr       string
	)
	err := row.Scan(&n.ID, &n.UserID, &fileID, &quoteID, &highlightID, &n.Title, &n.Body,
		&createdStr, &updatedStr)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan note: %w", err)
	}
	n.FileID = fileID.String
	n.QuoteID = quoteID.String
	n.HighlightID = highlightID.String
	if err := parseTime(createdStr, &n.CreatedAt); err != nil {
		return nil, err
	}
	if err := parseTime(updatedStr, &n.UpdatedAt); err != nil {
		return nil, err
	}
	return &n, nil
}

func scanNotes(rows *sql.Rows) ([]*Note, error) {
	var out []*Note
	for rows.Next() {
		n, err := scanNote(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// --- helpers ---

func nullable(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

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
