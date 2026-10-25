// Package savedwords manages the words a user looked up in the reader's
// dictionary and chose to keep, in a per-library SQLite database. Each
// keeps the meaning as it was read (so it outlives the dictionary it came
// from) and the reader location it was found at, following the same
// opaque-location convention as internal/highlights.
package savedwords

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrNotFound is returned when a saved word does not exist, or exists but
// is owned by a different user (the two are indistinguishable to the
// caller, so another user's words never leak their existence).
var ErrNotFound = errors.New("saved word not found")

// ErrInvalid is returned for a word or location that is empty or too long.
var ErrInvalid = errors.New("invalid saved word")

// Limits keep one careless request from storing a book's worth of text.
const (
	MaxWordRunes       = 64
	MaxTextRunes       = 512
	MaxDefinitionRunes = 8000
)

// Word is a saved dictionary word at a location in a file.
type Word struct {
	ID     string
	UserID string
	FileID string
	// Word is the word as looked up ("running"); Text is the exact text
	// selected in the book, which the reader finds again to paint it.
	Word       string
	Location   string
	Text       string
	Definition string
	// Source names the dictionary the definition came from.
	Source    string
	CreatedAt time.Time
}

// Store is the repository for saved words in a per-library database.
type Store struct {
	db *sql.DB
}

// NewStore wraps a per-library database pool.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Create saves w (its ID, UserID and CreatedAt are assigned here).
func (s *Store) Create(ctx context.Context, w Word) (*Word, error) {
	w.Word = strings.TrimSpace(w.Word)
	w.Text = strings.TrimSpace(w.Text)
	if w.Text == "" {
		w.Text = w.Word
	}
	if w.Word == "" || w.Location == "" ||
		len([]rune(w.Word)) > MaxWordRunes ||
		len([]rune(w.Text)) > MaxTextRunes ||
		len([]rune(w.Definition)) > MaxDefinitionRunes {
		return nil, ErrInvalid
	}
	id := newID()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO saved_words (id, user_id, file_id, word, location, text, definition, source, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, w.UserID, w.FileID, w.Word, w.Location, w.Text, w.Definition, w.Source,
		time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, fmt.Errorf("create saved word: %w", err)
	}
	return s.GetOwned(ctx, id, w.UserID)
}

// GetOwned returns a saved word by ID, scoped to its owner.
func (s *Store) GetOwned(ctx context.Context, id, userID string) (*Word, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, user_id, file_id, word, location, text, definition, source, created_at
		FROM saved_words
		WHERE id = ? AND user_id = ?`, id, userID)
	return scanWord(row)
}

// ListByFile returns userID's saved words in fileID, newest first.
func (s *Store) ListByFile(ctx context.Context, userID, fileID string) ([]*Word, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, file_id, word, location, text, definition, source, created_at
		FROM saved_words
		WHERE user_id = ? AND file_id = ?
		ORDER BY created_at DESC`, userID, fileID)
	if err != nil {
		return nil, fmt.Errorf("list saved words: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []*Word
	for rows.Next() {
		w, err := scanWord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// Delete removes a saved word owned by userID.
func (s *Store) Delete(ctx context.Context, id, userID string) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM saved_words WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return fmt.Errorf("delete saved word: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanWord(row rowScanner) (*Word, error) {
	var (
		w       Word
		created string
	)
	err := row.Scan(&w.ID, &w.UserID, &w.FileID, &w.Word, &w.Location, &w.Text,
		&w.Definition, &w.Source, &created)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan saved word: %w", err)
	}
	t, err := time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return nil, fmt.Errorf("parse time %q: %w", created, err)
	}
	w.CreatedAt = t
	return &w, nil
}

func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("fallback-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
