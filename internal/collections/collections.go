// Package collections manages personal, user-curated groupings of files in
// a per-library SQLite database. Unlike tags (a shared library vocabulary),
// a collection belongs to the user who created it.
package collections

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/Jishnu-Prasad888/Orchard/internal/media"
)

// ErrNotFound is returned when a collection does not exist, or exists but is
// owned by a different user (the two are indistinguishable to the caller,
// so another user's collection never leaks its existence).
var ErrNotFound = errors.New("collection not found")

// ErrAlreadyExists is returned when a name is already taken by one of the
// same user's collections.
var ErrAlreadyExists = errors.New("collection already exists")

// ErrFileNotInCollection is returned when trying to remove a file that is
// not a member of the collection.
var ErrFileNotInCollection = errors.New("file is not in collection")

// Collection is a named, personal grouping of files.
type Collection struct {
	ID          string
	UserID      string
	Name        string
	Description string
	Pinned      bool
	ItemCount   int
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Store is the repository for collections in a per-library database.
type Store struct {
	db        *sql.DB
	libraryID string
}

// NewStore wraps a per-library database pool.
func NewStore(db *sql.DB, libraryID string) *Store {
	return &Store{db: db, libraryID: libraryID}
}

// Create makes a new collection owned by userID.
func (s *Store) Create(ctx context.Context, userID, name, description string) (*Collection, error) {
	if name == "" {
		return nil, fmt.Errorf("collection name must not be empty")
	}
	id := newID()
	now := rfc3339(time.Now().UTC())
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO collections (id, user_id, name, description, pinned, created_at, updated_at)
		 VALUES (?, ?, ?, ?, 0, ?, ?)`,
		id, userID, name, description, now, now)
	if err != nil {
		if isUniqueConstraint(err) {
			return nil, ErrAlreadyExists
		}
		return nil, fmt.Errorf("create collection: %w", err)
	}
	return s.GetOwned(ctx, id, userID)
}

// GetOwned returns a collection by ID, scoped to its owner. Returns
// ErrNotFound both when the ID does not exist and when it belongs to a
// different user.
func (s *Store) GetOwned(ctx context.Context, id, userID string) (*Collection, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT c.id, c.user_id, c.name, c.description, c.pinned, c.created_at, c.updated_at,
		       (SELECT COUNT(*) FROM collection_items ci WHERE ci.collection_id = c.id)
		FROM collections c
		WHERE c.id = ? AND c.user_id = ?`, id, userID)
	return s.scanCollection(row)
}

// List returns all of userID's collections, ordered pinned-first then by
// name.
func (s *Store) List(ctx context.Context, userID string) ([]*Collection, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, c.user_id, c.name, c.description, c.pinned, c.created_at, c.updated_at,
		       (SELECT COUNT(*) FROM collection_items ci WHERE ci.collection_id = c.id)
		FROM collections c
		WHERE c.user_id = ?
		ORDER BY c.pinned DESC, c.name COLLATE NOCASE`, userID)
	if err != nil {
		return nil, fmt.Errorf("list collections: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return s.scanCollections(rows)
}

// Update describes a patch to a collection. Only non-nil fields are changed.
type Update struct {
	Name        *string
	Description *string
	Pinned      *bool
}

// Update applies a patch to a collection owned by userID.
func (s *Store) Update(ctx context.Context, id, userID string, patch Update) (*Collection, error) {
	existing, err := s.GetOwned(ctx, id, userID)
	if err != nil {
		return nil, err
	}
	name := existing.Name
	if patch.Name != nil {
		name = *patch.Name
	}
	description := existing.Description
	if patch.Description != nil {
		description = *patch.Description
	}
	pinned := existing.Pinned
	if patch.Pinned != nil {
		pinned = *patch.Pinned
	}
	if name == "" {
		return nil, fmt.Errorf("collection name must not be empty")
	}
	now := rfc3339(time.Now().UTC())
	_, err = s.db.ExecContext(ctx,
		`UPDATE collections SET name = ?, description = ?, pinned = ?, updated_at = ?
		 WHERE id = ? AND user_id = ?`,
		name, description, boolToInt(pinned), now, id, userID)
	if err != nil {
		if isUniqueConstraint(err) {
			return nil, ErrAlreadyExists
		}
		return nil, fmt.Errorf("update collection: %w", err)
	}
	return s.GetOwned(ctx, id, userID)
}

// Delete removes a collection and its file associations. Returns
// ErrNotFound if id does not exist or is not owned by userID.
func (s *Store) Delete(ctx context.Context, id, userID string) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM collections WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return fmt.Errorf("delete collection: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// AddFile adds a file to a collection owned by userID. No-op if already a
// member. Returns ErrNotFound if the collection does not exist or is not
// owned by userID.
func (s *Store) AddFile(ctx context.Context, collectionID, userID, fileID string) error {
	if _, err := s.GetOwned(ctx, collectionID, userID); err != nil {
		return err
	}
	now := rfc3339(time.Now().UTC())
	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO collection_items (collection_id, file_id, added_at) VALUES (?, ?, ?)`,
		collectionID, fileID, now)
	if err != nil {
		return fmt.Errorf("add file to collection: %w", err)
	}
	return nil
}

// RemoveFile removes a file from a collection owned by userID.
func (s *Store) RemoveFile(ctx context.Context, collectionID, userID, fileID string) error {
	if _, err := s.GetOwned(ctx, collectionID, userID); err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM collection_items WHERE collection_id = ? AND file_id = ?`, collectionID, fileID)
	if err != nil {
		return fmt.Errorf("remove file from collection: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrFileNotInCollection
	}
	return nil
}

// ListFiles returns the present files in a collection owned by userID,
// newest-added first.
func (s *Store) ListFiles(ctx context.Context, collectionID, userID string) ([]*media.File, error) {
	if _, err := s.GetOwned(ctx, collectionID, userID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT f.id, f.rel_path, f.size_bytes, f.mod_time, f.content_hash,
		       f.status, f.first_seen_at, f.last_seen_at
		FROM indexed_files f
		JOIN collection_items ci ON ci.file_id = f.id
		WHERE f.status = 'present' AND ci.collection_id = ?
		ORDER BY ci.added_at DESC`, collectionID)
	if err != nil {
		return nil, fmt.Errorf("list collection files: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return s.scanFiles(rows)
}

// ListByFile returns userID's collections that contain fileID — used by the
// "add to collection" picker to show which collections already have a file.
func (s *Store) ListByFile(ctx context.Context, userID, fileID string) ([]*Collection, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, c.user_id, c.name, c.description, c.pinned, c.created_at, c.updated_at,
		       (SELECT COUNT(*) FROM collection_items ci2 WHERE ci2.collection_id = c.id)
		FROM collections c
		JOIN collection_items ci ON ci.collection_id = c.id
		WHERE c.user_id = ? AND ci.file_id = ?
		ORDER BY c.name COLLATE NOCASE`, userID, fileID)
	if err != nil {
		return nil, fmt.Errorf("list collections by file: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return s.scanCollections(rows)
}

// --- row scanners ---

type rowScanner interface {
	Scan(dest ...any) error
}

func (s *Store) scanCollection(row rowScanner) (*Collection, error) {
	var (
		c                      Collection
		pinned                 int
		createdStr, updatedStr string
	)
	err := row.Scan(&c.ID, &c.UserID, &c.Name, &c.Description, &pinned,
		&createdStr, &updatedStr, &c.ItemCount)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan collection: %w", err)
	}
	c.Pinned = pinned != 0
	if err := parseTime(createdStr, &c.CreatedAt); err != nil {
		return nil, err
	}
	if err := parseTime(updatedStr, &c.UpdatedAt); err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Store) scanCollections(rows *sql.Rows) ([]*Collection, error) {
	var out []*Collection
	for rows.Next() {
		c, err := s.scanCollection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
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

func isUniqueConstraint(err error) bool {
	if err == nil {
		return false
	}
	return containsAny(err.Error(), "UNIQUE constraint failed", "unique constraint failed")
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(s) >= len(sub) {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
		}
	}
	return false
}
