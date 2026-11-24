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

// MaxPinned is how many collections one user can pin at a time.
const MaxPinned = 5

// ErrTooManyPinned is returned when pinning one more than MaxPinned.
var ErrTooManyPinned = errors.New("too many pinned collections")

// ErrInvalidColor is returned for a colour outside Colors.
var ErrInvalidColor = errors.New("unknown collection colour")

// Colors is the fixed palette a collection can be coloured with, by name;
// the web app owns what each looks like in light and dark themes. No
// colour is "".
var Colors = []string{
	"coral", "peach", "sand", "mint", "sage", "fog", "storm", "dusk", "blossom", "clay", "chalk",
}

// ValidColor reports whether c is "" or one of Colors.
func ValidColor(c string) bool {
	if c == "" {
		return true
	}
	for _, known := range Colors {
		if c == known {
			return true
		}
	}
	return false
}

// Collection is a named, personal grouping of files.
type Collection struct {
	ID          string
	UserID      string
	Name        string
	Description string
	Pinned      bool
	Color       string
	// PinnedAt is when it was last pinned; zero while unpinned.
	PinnedAt time.Time
	// Hidden keeps it off the user's Collections page unless searched for.
	Hidden    bool
	ItemCount int
	CreatedAt time.Time
	UpdatedAt time.Time
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
		SELECT c.id, c.user_id, c.name, c.description, c.pinned, c.color, c.pinned_at, c.hidden, c.created_at, c.updated_at,
		       (SELECT COUNT(*) FROM collection_items ci WHERE ci.collection_id = c.id)
		FROM collections c
		WHERE c.id = ? AND c.user_id = ?`, id, userID)
	return s.scanCollection(row)
}

// List returns all of userID's collections: the pinned ones first, most
// recently pinned first, then the rest by name.
func (s *Store) List(ctx context.Context, userID string) ([]*Collection, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, c.user_id, c.name, c.description, c.pinned, c.color, c.pinned_at, c.hidden, c.created_at, c.updated_at,
		       (SELECT COUNT(*) FROM collection_items ci WHERE ci.collection_id = c.id)
		FROM collections c
		WHERE c.user_id = ?
		ORDER BY c.pinned DESC, c.pinned_at DESC, c.name COLLATE NOCASE`, userID)
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
	Color       *string
	Hidden      *bool
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
	color := existing.Color
	if patch.Color != nil {
		color = *patch.Color
	}
	if name == "" {
		return nil, fmt.Errorf("collection name must not be empty")
	}
	if !ValidColor(color) {
		return nil, ErrInvalidColor
	}
	hidden := existing.Hidden
	if patch.Hidden != nil {
		hidden = *patch.Hidden
	}
	// A hidden collection isn't shown anywhere, Home included, so hiding
	// one unpins it (and frees its pin).
	if hidden && patch.Hidden != nil {
		pinned = false
	}
	if pinned && !existing.Pinned {
		var count int
		if err := s.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM collections WHERE user_id = ? AND pinned != 0`, userID).Scan(&count); err != nil {
			return nil, fmt.Errorf("count pinned collections: %w", err)
		}
		if count >= MaxPinned {
			return nil, ErrTooManyPinned
		}
	}
	now := rfc3339(time.Now().UTC())
	// Pinning (again) puts it first; unpinning forgets when.
	var pinnedAt any
	switch {
	case pinned && !existing.Pinned:
		pinnedAt = sortableTime(time.Now())
	case pinned:
		pinnedAt = sortableTime(existing.PinnedAt)
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE collections SET name = ?, description = ?, pinned = ?, color = ?, pinned_at = ?,
		        hidden = ?, updated_at = ?
		 WHERE id = ? AND user_id = ?`,
		name, description, boolToInt(pinned), color, pinnedAt, boolToInt(hidden), now, id, userID)
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

// Timeline returns the ids of the files on a collection's reading timeline,
// in reading order. Files no longer present are left out.
func (s *Store) Timeline(ctx context.Context, collectionID, userID string) ([]string, error) {
	if _, err := s.GetOwned(ctx, collectionID, userID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT ci.file_id
		FROM collection_items ci
		JOIN indexed_files f ON f.id = ci.file_id
		WHERE ci.collection_id = ? AND ci.position IS NOT NULL AND f.status = 'present'
		ORDER BY ci.position, ci.added_at`, collectionID)
	if err != nil {
		return nil, fmt.Errorf("list collection timeline: %w", err)
	}
	defer func() { _ = rows.Close() }()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan collection timeline: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// SetTimeline replaces a collection's reading timeline with fileIDs, read in
// that order. Every id must already be in the collection (ErrFileNotInCollection
// otherwise); items not listed leave the timeline. Duplicates keep their
// first place.
func (s *Store) SetTimeline(ctx context.Context, collectionID, userID string, fileIDs []string) error {
	if _, err := s.GetOwned(ctx, collectionID, userID); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin set timeline: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx,
		`UPDATE collection_items SET position = NULL WHERE collection_id = ?`, collectionID); err != nil {
		return fmt.Errorf("clear collection timeline: %w", err)
	}
	seen := make(map[string]bool, len(fileIDs))
	position := 0
	for _, id := range fileIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		position++
		res, err := tx.ExecContext(ctx,
			`UPDATE collection_items SET position = ? WHERE collection_id = ? AND file_id = ?`,
			position, collectionID, id)
		if err != nil {
			return fmt.Errorf("set collection timeline: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrFileNotInCollection
		}
	}
	return tx.Commit()
}

// ListByFile returns userID's collections that contain fileID — used by the
// "add to collection" picker to show which collections already have a file.
func (s *Store) ListByFile(ctx context.Context, userID, fileID string) ([]*Collection, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, c.user_id, c.name, c.description, c.pinned, c.color, c.pinned_at, c.hidden, c.created_at, c.updated_at,
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
		pinned, hidden         int
		pinnedAt               sql.NullString
		createdStr, updatedStr string
	)
	err := row.Scan(&c.ID, &c.UserID, &c.Name, &c.Description, &pinned, &c.Color, &pinnedAt, &hidden,
		&createdStr, &updatedStr, &c.ItemCount)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan collection: %w", err)
	}
	c.Pinned = pinned != 0
	c.Hidden = hidden != 0
	if c.Pinned && pinnedAt.Valid {
		if err := parseTime(pinnedAt.String, &c.PinnedAt); err != nil {
			return nil, err
		}
	}
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

// sortableTime is RFC 3339 with all nine fractional digits, so the column
// sorts as text in time order (RFC3339Nano drops trailing zeros, and
// "…05.1Z" would sort after "…05.12Z").
func sortableTime(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000000000Z07:00") }

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
