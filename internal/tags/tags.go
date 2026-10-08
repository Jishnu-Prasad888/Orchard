// Package tags manages user-defined labels (tags) for indexed files in a
// per-library SQLite database.
package tags

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// ErrNotFound is returned when a tag does not exist.
var ErrNotFound = errors.New("tag not found")

// ErrAlreadyExists is returned when a tag name is already taken.
var ErrAlreadyExists = errors.New("tag already exists")

// ErrFileNotTagged is returned when trying to detach a tag that is not attached.
var ErrFileNotTagged = errors.New("tag is not attached to file")

// Tag represents a named label.
type Tag struct {
	ID        string
	Name      string
	Color     string // optional hex color, e.g. "#ff5500"
	CreatedAt time.Time
}

// TagStore is the repository for tags in a per-library database.
type TagStore struct {
	db *sql.DB
}

// NewTagStore wraps a per-library database pool.
func NewTagStore(db *sql.DB) *TagStore {
	return &TagStore{db: db}
}

// Create inserts a new tag. Returns ErrAlreadyExists if the name is taken
// (case-insensitive).
func (s *TagStore) Create(ctx context.Context, name, color string) (*Tag, error) {
	if name == "" {
		return nil, fmt.Errorf("tag name must not be empty")
	}
	id := newID()
	now := rfc3339(time.Now().UTC())
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO tags (id, name, color, created_at) VALUES (?, ?, ?, ?)`,
		id, name, nullStr(color), now)
	if err != nil {
		if isUniqueConstraint(err) {
			return nil, ErrAlreadyExists
		}
		return nil, fmt.Errorf("create tag: %w", err)
	}
	return s.GetByID(ctx, id)
}

// GetByID returns a tag by its ID.
func (s *TagStore) GetByID(ctx context.Context, id string) (*Tag, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, name, color, created_at FROM tags WHERE id = ?`, id)
	return s.scanTag(row)
}

// GetByName returns a tag by its name (case-insensitive).
func (s *TagStore) GetByName(ctx context.Context, name string) (*Tag, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, name, color, created_at FROM tags WHERE name = ? COLLATE NOCASE`, name)
	return s.scanTag(row)
}

// List returns all tags ordered by name.
func (s *TagStore) List(ctx context.Context) ([]*Tag, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, color, created_at FROM tags ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, fmt.Errorf("list tags: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return s.scanTags(rows)
}

// Delete removes a tag and all its file associations.
func (s *TagStore) Delete(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM tags WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete tag: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Attach associates a tag with a file. No-op if already attached.
func (s *TagStore) Attach(ctx context.Context, fileID, tagID string) error {
	now := rfc3339(time.Now().UTC())
	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO file_tags (file_id, tag_id, created_at) VALUES (?, ?, ?)`,
		fileID, tagID, now)
	if err != nil {
		return fmt.Errorf("attach tag: %w", err)
	}
	return nil
}

// Detach removes a tag association from a file.
func (s *TagStore) Detach(ctx context.Context, fileID, tagID string) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM file_tags WHERE file_id = ? AND tag_id = ?`, fileID, tagID)
	if err != nil {
		return fmt.Errorf("detach tag: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrFileNotTagged
	}
	return nil
}

// ListByFile returns all tags attached to a given file.
func (s *TagStore) ListByFile(ctx context.Context, fileID string) ([]*Tag, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT t.id, t.name, t.color, t.created_at
		 FROM tags t
		 JOIN file_tags ft ON ft.tag_id = t.id
		 WHERE ft.file_id = ?
		 ORDER BY t.name COLLATE NOCASE`, fileID)
	if err != nil {
		return nil, fmt.Errorf("list tags by file: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return s.scanTags(rows)
}

// ListByTag returns the IDs of all files associated with a tag.
func (s *TagStore) ListByTag(ctx context.Context, tagID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT file_id FROM file_tags WHERE tag_id = ? ORDER BY file_id`, tagID)
	if err != nil {
		return nil, fmt.Errorf("list files by tag: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// --- row scanners ---

type rowScanner interface {
	Scan(dest ...any) error
}

func (s *TagStore) scanTag(row rowScanner) (*Tag, error) {
	var (
		t          Tag
		color      sql.NullString
		createdStr string
	)
	err := row.Scan(&t.ID, &t.Name, &color, &createdStr)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan tag: %w", err)
	}
	t.Color = color.String
	if err := parseTime(createdStr, &t.CreatedAt); err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *TagStore) scanTags(rows *sql.Rows) ([]*Tag, error) {
	var out []*Tag
	for rows.Next() {
		t, err := s.scanTag(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
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

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// isUniqueConstraint returns true if err is a SQLite UNIQUE constraint violation.
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
