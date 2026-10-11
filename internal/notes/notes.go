// Package notes manages per-file Markdown notes (captions and descriptions)
// in a per-library SQLite database.
package notes

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Note is a per-file, per-user Markdown note attached to one file in a library.
type Note struct {
	FileID    string
	UserID    string
	Body      string
	UpdatedAt time.Time
}

// NoteStore is the repository for per-file notes in a per-library database.
type NoteStore struct {
	db        *sql.DB
	libraryID string
}

// NewNoteStore wraps a per-library database pool.
func NewNoteStore(db *sql.DB, libraryID string) *NoteStore {
	return &NoteStore{db: db, libraryID: libraryID}
}

// Get loads the given user's note for a file. When the user has no note yet
// it returns a Note with an empty Body and a zero UpdatedAt (never an
// error), so callers can treat notes as optional captions.
func (s *NoteStore) Get(ctx context.Context, userID, fileID string) (Note, error) {
	var (
		body string
		upd  string
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT body, updated_at FROM file_notes WHERE user_id = ? AND file_id = ?`, userID, fileID).
		Scan(&body, &upd)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Note{FileID: fileID, UserID: userID}, nil
		}
		return Note{}, fmt.Errorf("get note: %w", err)
	}
	t, err := time.Parse(time.RFC3339Nano, upd)
	if err != nil {
		return Note{}, fmt.Errorf("parse note updated_at: %w", err)
	}
	return Note{FileID: fileID, UserID: userID, Body: body, UpdatedAt: t}, nil
}

// Set upserts the given user's Markdown body for a file and returns the
// stored note.
func (s *NoteStore) Set(ctx context.Context, userID, fileID, body string) (Note, error) {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO file_notes (user_id, file_id, body, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(user_id, file_id) DO UPDATE SET
			body = excluded.body,
			updated_at = excluded.updated_at`,
		userID, fileID, body, now.Format(time.RFC3339Nano))
	if err != nil {
		return Note{}, fmt.Errorf("set note: %w", err)
	}
	return Note{FileID: fileID, UserID: userID, Body: body, UpdatedAt: now}, nil
}

// Clear removes the given user's note for a file so a later Get returns an
// empty body. Clearing a note that does not exist is not an error.
func (s *NoteStore) Clear(ctx context.Context, userID, fileID string) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM file_notes WHERE user_id = ? AND file_id = ?`, userID, fileID); err != nil {
		return fmt.Errorf("clear note: %w", err)
	}
	return nil
}
