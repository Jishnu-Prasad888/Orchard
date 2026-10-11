// Package bookmeta extracts and stores book-style metadata (title, author,
// cover, extracted plain text) for document files in a library.
//
// It plugs into internal/indexer's MediaProcessor extension point: every
// newly indexed or modified document file gets one process_media job, and
// Processor.Handle derives whatever this package knows how to derive from
// its extension (EPUB and PDF today; anything else just gets a filename-
// derived title so every document still has an editable book_metadata row).
//
// Covers and extracted text are written under the library's .orchard/
// folder as cover_path/text_path, relative to that folder — never into the
// original file or outside the library root — so the originals stay
// untouched and the paths stay portable if the library is remounted
// elsewhere.
package bookmeta

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Metadata is a document's book-style metadata, one row per indexed file.
type Metadata struct {
	FileID      string
	Title       string
	Author      string
	Publisher   string
	Year        string
	ISBN        string
	Description string
	// CoverPath and TextPath are paths relative to the library's .orchard/
	// folder (e.g. "covers/<file_id>.jpg"), empty when nothing was extracted.
	CoverPath string
	TextPath  string
	// UserEdited is true once a person has edited this file's metadata or
	// cover from the book details panel. Upsert (the extraction path) then
	// leaves the row alone rather than overwriting their edit on a re-scan.
	UserEdited  bool
	ExtractedAt time.Time
}

// UserEdit is a set of book-metadata fields a person submitted from the
// book details panel. Pointer fields are patch semantics: nil leaves the
// stored value unchanged, so a caller can send only the fields that changed.
type UserEdit struct {
	Title       *string
	Author      *string
	Publisher   *string
	Year        *string
	ISBN        *string
	Description *string
}

// Store is the repository for book metadata in a per-library database.
type Store struct {
	db *sql.DB
}

// NewStore wraps a per-library database pool.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Get loads the metadata for a file. ok is false when the file has not been
// processed yet (no row exists).
func (s *Store) Get(ctx context.Context, fileID string) (Metadata, bool, error) {
	var (
		m          Metadata
		ext        string
		userEdited int
	)
	m.FileID = fileID
	err := s.db.QueryRowContext(ctx,
		`SELECT title, author, publisher, year, isbn, description, cover_path, text_path, user_edited, extracted_at
		 FROM book_metadata WHERE file_id = ?`, fileID).
		Scan(&m.Title, &m.Author, &m.Publisher, &m.Year, &m.ISBN, &m.Description,
			&m.CoverPath, &m.TextPath, &userEdited, &ext)
	if err != nil {
		if err == sql.ErrNoRows {
			return Metadata{}, false, nil
		}
		return Metadata{}, false, fmt.Errorf("get book metadata: %w", err)
	}
	m.UserEdited = userEdited != 0
	if t, perr := time.Parse(time.RFC3339Nano, ext); perr == nil {
		m.ExtractedAt = t
	}
	return m, true, nil
}

// Upsert replaces the extracted metadata for a file. It is extraction-only:
// once a person has edited the row (UserEdited), this leaves it alone
// rather than overwriting their edit on a re-scan — the WHERE on the
// conflict clause makes that a single statement rather than a race-prone
// read-then-write.
func (s *Store) Upsert(ctx context.Context, m Metadata) error {
	extractedAt := m.ExtractedAt
	if extractedAt.IsZero() {
		extractedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO book_metadata
			(file_id, title, author, publisher, year, isbn, description, cover_path, text_path, user_edited, extracted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?)
		ON CONFLICT(file_id) DO UPDATE SET
			title        = excluded.title,
			author       = excluded.author,
			publisher    = excluded.publisher,
			year         = excluded.year,
			isbn         = excluded.isbn,
			description  = excluded.description,
			cover_path   = excluded.cover_path,
			text_path    = excluded.text_path,
			extracted_at = excluded.extracted_at
		WHERE book_metadata.user_edited = 0`,
		m.FileID, m.Title, m.Author, m.Publisher, m.Year, m.ISBN, m.Description,
		m.CoverPath, m.TextPath, extractedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("upsert book metadata: %w", err)
	}
	return nil
}

// SetUserEdit merges a person's edits from the book details panel into a
// file's metadata and marks the row user_edited, so Upsert leaves it alone
// on a future re-scan. Creates the row (from blank fields) when extraction
// has not run for this file yet.
func (s *Store) SetUserEdit(ctx context.Context, fileID string, edit UserEdit) (Metadata, error) {
	m, _, err := s.Get(ctx, fileID)
	if err != nil {
		return Metadata{}, err
	}
	m.FileID = fileID
	if edit.Title != nil {
		m.Title = *edit.Title
	}
	if edit.Author != nil {
		m.Author = *edit.Author
	}
	if edit.Publisher != nil {
		m.Publisher = *edit.Publisher
	}
	if edit.Year != nil {
		m.Year = *edit.Year
	}
	if edit.ISBN != nil {
		m.ISBN = *edit.ISBN
	}
	if edit.Description != nil {
		m.Description = *edit.Description
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO book_metadata
			(file_id, title, author, publisher, year, isbn, description, cover_path, text_path, user_edited, extracted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?)
		ON CONFLICT(file_id) DO UPDATE SET
			title       = excluded.title,
			author      = excluded.author,
			publisher   = excluded.publisher,
			year        = excluded.year,
			isbn        = excluded.isbn,
			description = excluded.description,
			user_edited = 1`,
		fileID, m.Title, m.Author, m.Publisher, m.Year, m.ISBN, m.Description,
		m.CoverPath, m.TextPath, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return Metadata{}, fmt.Errorf("set user edit: %w", err)
	}
	m.UserEdited = true
	return m, nil
}

// SetCover stores a custom cover image's path (relative to the library's
// .orchard/ folder) and marks the row user_edited. Creates the row when
// extraction has not run for this file yet.
func (s *Store) SetCover(ctx context.Context, fileID, coverRelPath string) (Metadata, error) {
	m, _, err := s.Get(ctx, fileID)
	if err != nil {
		return Metadata{}, err
	}
	m.FileID = fileID
	m.CoverPath = coverRelPath

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO book_metadata (file_id, title, cover_path, user_edited, extracted_at)
		VALUES (?, ?, ?, 1, ?)
		ON CONFLICT(file_id) DO UPDATE SET cover_path = excluded.cover_path, user_edited = 1`,
		fileID, m.Title, coverRelPath, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return Metadata{}, fmt.Errorf("set cover: %w", err)
	}
	m.UserEdited = true
	return m, nil
}
