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
	CoverPath   string
	TextPath    string
	ExtractedAt time.Time
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
		m   Metadata
		ext string
	)
	m.FileID = fileID
	err := s.db.QueryRowContext(ctx,
		`SELECT title, author, publisher, year, isbn, description, cover_path, text_path, extracted_at
		 FROM book_metadata WHERE file_id = ?`, fileID).
		Scan(&m.Title, &m.Author, &m.Publisher, &m.Year, &m.ISBN, &m.Description, &m.CoverPath, &m.TextPath, &ext)
	if err != nil {
		if err == sql.ErrNoRows {
			return Metadata{}, false, nil
		}
		return Metadata{}, false, fmt.Errorf("get book metadata: %w", err)
	}
	if t, perr := time.Parse(time.RFC3339Nano, ext); perr == nil {
		m.ExtractedAt = t
	}
	return m, true, nil
}

// Upsert replaces the stored metadata for a file.
//
// This is extraction-only: it always overwrites the row. Once Phase 1 adds
// a user-facing metadata edit modal, a manual edit must not be clobbered by
// the next re-extraction (e.g. a file replaced on disk) — that will need its
// own "user overridden" distinction, which does not exist yet.
func (s *Store) Upsert(ctx context.Context, m Metadata) error {
	extractedAt := m.ExtractedAt
	if extractedAt.IsZero() {
		extractedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO book_metadata
			(file_id, title, author, publisher, year, isbn, description, cover_path, text_path, extracted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(file_id) DO UPDATE SET
			title        = excluded.title,
			author       = excluded.author,
			publisher    = excluded.publisher,
			year         = excluded.year,
			isbn         = excluded.isbn,
			description  = excluded.description,
			cover_path   = excluded.cover_path,
			text_path    = excluded.text_path,
			extracted_at = excluded.extracted_at`,
		m.FileID, m.Title, m.Author, m.Publisher, m.Year, m.ISBN, m.Description,
		m.CoverPath, m.TextPath, extractedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("upsert book metadata: %w", err)
	}
	return nil
}
