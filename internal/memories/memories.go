// Package memories manages notebook-style memories in a per-library SQLite
// database.
//
// A memory is an ordered list of blocks (docs/adr/0015-memory-block-documents.md):
//
//	Memory ── metadata (title, date, description, location, cover, tags)
//	   └── blocks[]
//	         ├── text block  → Markdown
//	         └── image block → layout + slideshow + images[]
//	                               └── image → library media reference +
//	                                           caption + non-destructive edits
//
// Memories are app-level documents that live alongside the library so they are
// portable with it. Every mutation bumps memories.revision (optimistic
// concurrency) and records a version snapshot in memory_versions, so the edit
// history is preserved. The legacy single-body API (Create/Update with a
// Markdown body) still works: a body maps to one text block.
//
// Internal [[type:id]] references are parsed from text blocks on each save and
// stored in memory_refs together with a media ref for every referenced image.
package memories

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Jishnu-Prasad888/Cairn/internal/fts"
	"github.com/Jishnu-Prasad888/Cairn/internal/markdown"
)

// ErrNotFound is returned when a memory does not exist.
var ErrNotFound = errors.New("memory not found")

// ErrVersionNotFound is returned when a memory version does not exist.
var ErrVersionNotFound = errors.New("memory version not found")

// ErrDeleted is returned when mutating a soft-deleted memory.
var ErrDeleted = errors.New("memory is deleted")

// ErrStructured is returned by the legacy single-body Update when the memory
// has more than one block: replacing the whole document with one Markdown
// string would silently drop image sections.
var ErrStructured = errors.New("memory has structured blocks; update it through the document API")

// ValidationError indicates the caller supplied an invalid memory
// (for example a blank title).
type ValidationError struct {
	msg string
}

func (e *ValidationError) Error() string { return e.msg }

// ConflictError is returned when a write names a base revision that is no
// longer current: someone (another tab, device, or client) saved first.
type ConflictError struct {
	Current int
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("memory was modified (current revision %d)", e.Current)
}

// Memory is a single memory document. Blocks is only populated by
// GetDocument and SaveDocument; list endpoints leave it nil.
type Memory struct {
	ID          string
	Title       string
	Body        string // Markdown of all text blocks, joined by a blank line
	Description string
	Location    string
	CoverFileID string
	MemoryDate  *time.Time
	Tags        []string
	Revision    int
	Deleted     bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Blocks      []*Block
}

// MemoryVersion is one saved revision of a memory. Document is the JSON
// snapshot of the blocks (empty for versions written before schema v8).
type MemoryVersion struct {
	MemoryID string
	Version  int
	Title    string
	Body     string
	Document string
	SavedAt  time.Time
}

// CreateParams are the fields required to create a memory. A non-empty Body
// becomes the first text block; Blocks (if given) takes precedence.
type CreateParams struct {
	Title      string
	Body       string
	MemoryDate *time.Time
	Blocks     []*Block
}

// UpdateParams are the fields that can change on a legacy update. A nil
// MemoryDate leaves the existing date unchanged.
type UpdateParams struct {
	Title      string
	Body       string
	MemoryDate *time.Time
	// ClearDate removes an existing memory date when true.
	ClearDate bool
}

// MetaPatch updates memory metadata. Nil fields are left unchanged.
type MetaPatch struct {
	Title       *string
	Description *string
	Location    *string
	CoverFileID *string // "" clears the cover
	MemoryDate  *time.Time
	ClearDate   bool
	Tags        *[]string // tag names; missing tags are created
}

// MemoryStore is the repository for memories in a per-library database.
type MemoryStore struct {
	db *sql.DB
	// now is swappable for tests (version coalescing is time based).
	now func() time.Time
}

// NewMemoryStore wraps a per-library database pool.
func NewMemoryStore(db *sql.DB) *MemoryStore {
	return &MemoryStore{db: db, now: func() time.Time { return time.Now().UTC() }}
}

const memoryColumns = `id, title, body, description, location, cover_file_id, memory_date,
	revision, deleted, created_at, updated_at`

// Create inserts a new memory and records its first version.
func (s *MemoryStore) Create(ctx context.Context, p CreateParams) (*Memory, error) {
	if strings.TrimSpace(p.Title) == "" {
		return nil, &ValidationError{msg: "memory requires a title"}
	}
	blocks := p.Blocks
	if blocks == nil {
		blocks = []*Block{{Type: BlockText, Markdown: p.Body}}
	}
	if err := validateBlocks(blocks); err != nil {
		return nil, err
	}

	id := newID()
	now := rfc3339(s.now())

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin create memory: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	_, err = tx.ExecContext(ctx,
		`INSERT INTO memories (id, title, body, memory_date, deleted, revision, blocks_migrated, created_at, updated_at)
		 VALUES (?, ?, '', ?, 0, 0, 1, ?, ?)`,
		id, p.Title, optionalTime(p.MemoryDate), now, now)
	if err != nil {
		return nil, fmt.Errorf("insert memory: %w", err)
	}
	if err := writeBlocksTx(ctx, tx, id, blocks, now); err != nil {
		return nil, err
	}
	if _, err := s.finalizeTx(ctx, tx, id, now, false); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit create memory: %w", err)
	}
	return s.Get(ctx, id)
}

// Get returns a memory's metadata by ID (without blocks). Deleted memories are
// returned with Deleted=true so callers can present a restore path.
func (s *MemoryStore) Get(ctx context.Context, id string) (*Memory, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+memoryColumns+` FROM memories WHERE id = ?`, id)
	m, err := scanMemory(row)
	if err != nil {
		return nil, err
	}
	tags, err := listTags(ctx, s.db, id)
	if err != nil {
		return nil, err
	}
	m.Tags = tags
	return m, nil
}

// GetDocument returns a memory with its ordered blocks and images.
func (s *MemoryStore) GetDocument(ctx context.Context, id string) (*Memory, error) {
	m, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	blocks, err := loadBlocks(ctx, s.db, id)
	if err != nil {
		return nil, err
	}
	m.Blocks = blocks
	return m, nil
}

// Update is the legacy whole-body save. It replaces the memory's single text
// block with Body. It refuses (ErrStructured) when the memory has image blocks
// or several text blocks, because one Markdown string cannot represent them.
func (s *MemoryStore) Update(ctx context.Context, id string, p UpdateParams) (*Memory, error) {
	if strings.TrimSpace(p.Title) == "" {
		return nil, &ValidationError{msg: "memory requires a title"}
	}
	now := rfc3339(s.now())

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin update memory: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := lockMemory(ctx, tx, id, nil); err != nil {
		return nil, err
	}
	blocks, err := loadBlocks(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	switch {
	case len(blocks) == 0:
		blocks = []*Block{{Type: BlockText, Markdown: p.Body}}
	case len(blocks) == 1 && blocks[0].Type == BlockText:
		blocks[0].Markdown = p.Body
	default:
		return nil, ErrStructured
	}
	if err := writeBlocksTx(ctx, tx, id, blocks, now); err != nil {
		return nil, err
	}

	set := `title = ?`
	args := []any{p.Title}
	if p.ClearDate {
		set += `, memory_date = NULL`
	} else if p.MemoryDate != nil {
		set += `, memory_date = ?`
		args = append(args, rfc3339(*p.MemoryDate))
	}
	args = append(args, id)
	if _, err := tx.ExecContext(ctx, `UPDATE memories SET `+set+` WHERE id = ?`, args...); err != nil {
		return nil, fmt.Errorf("update memory: %w", err)
	}
	if _, err := s.finalizeTx(ctx, tx, id, now, false); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit update memory: %w", err)
	}
	return s.Get(ctx, id)
}

// PatchMeta updates memory metadata (title, description, location, date,
// cover and tags). baseRevision, when non-nil, must equal the current
// revision.
func (s *MemoryStore) PatchMeta(ctx context.Context, id string, baseRevision *int, p MetaPatch) (*Memory, error) {
	now := rfc3339(s.now())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin patch memory: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := lockMemory(ctx, tx, id, baseRevision); err != nil {
		return nil, err
	}

	var sets []string
	var args []any
	if p.Title != nil {
		if strings.TrimSpace(*p.Title) == "" {
			return nil, &ValidationError{msg: "memory requires a title"}
		}
		if len(*p.Title) > 500 {
			return nil, &ValidationError{msg: "title is too long (max 500 characters)"}
		}
		sets = append(sets, "title = ?")
		args = append(args, *p.Title)
	}
	if p.Description != nil {
		if len(*p.Description) > 10000 {
			return nil, &ValidationError{msg: "description is too long"}
		}
		sets = append(sets, "description = ?")
		args = append(args, *p.Description)
	}
	if p.Location != nil {
		if len(*p.Location) > 500 {
			return nil, &ValidationError{msg: "location is too long"}
		}
		sets = append(sets, "location = ?")
		args = append(args, *p.Location)
	}
	if p.CoverFileID != nil {
		sets = append(sets, "cover_file_id = ?")
		args = append(args, nullIfEmpty(*p.CoverFileID))
	}
	if p.ClearDate {
		sets = append(sets, "memory_date = NULL")
	} else if p.MemoryDate != nil {
		sets = append(sets, "memory_date = ?")
		args = append(args, rfc3339(*p.MemoryDate))
	}
	if len(sets) > 0 {
		args = append(args, id)
		if _, err := tx.ExecContext(ctx, `UPDATE memories SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...); err != nil {
			return nil, fmt.Errorf("patch memory: %w", err)
		}
	}
	if p.Tags != nil {
		if err := replaceTagsTx(ctx, tx, id, *p.Tags, now); err != nil {
			return nil, err
		}
	}
	if _, err := s.finalizeTx(ctx, tx, id, now, true); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit patch memory: %w", err)
	}
	return s.Get(ctx, id)
}

// Delete soft-deletes a memory. Its blocks, versions, references and derived
// media are retained so the memory can be recovered later; List and Search
// exclude it.
func (s *MemoryStore) Delete(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE memories SET deleted = 1, updated_at = ? WHERE id = ?`,
		rfc3339(s.now()), id)
	if err != nil {
		return fmt.Errorf("delete memory: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Restore clears the soft-delete flag on a memory.
func (s *MemoryStore) Restore(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE memories SET deleted = 0, updated_at = ? WHERE id = ?`,
		rfc3339(s.now()), id)
	if err != nil {
		return fmt.Errorf("restore memory: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// List returns non-deleted memories ordered by most recently updated first,
// with keyset pagination. The second return value is the cursor for the next
// page (empty when there are no further items).
func (s *MemoryStore) List(ctx context.Context, cursor string, limit int) ([]*Memory, string, error) {
	limit = clampLimit(limit)
	query := `SELECT ` + memoryColumns + ` FROM memories WHERE deleted = 0`
	args := []any{}
	if cursor != "" {
		updatedAt, id, ok := decodeCursor(cursor)
		if ok {
			query += ` AND (updated_at < ? OR (updated_at = ? AND id < ?))`
			args = append(args, updatedAt, updatedAt, id)
		}
	}
	query += ` ORDER BY updated_at DESC, id DESC LIMIT ?`
	args = append(args, limit+1)
	return s.queryPage(ctx, query, args, limit)
}

// Search runs a full-text query over memory titles, text blocks, captions,
// description, location and tags, and returns matching non-deleted memories,
// most recently updated first, plus the cursor for the next page.
func (s *MemoryStore) Search(ctx context.Context, q, cursor string, limit int) ([]*Memory, string, error) {
	limit = clampLimit(limit)
	expr, err := fts.BuildExpression(q)
	if err != nil {
		return nil, "", err
	}
	cols := prefixed("m.", memoryColumns)
	query := `SELECT ` + cols + ` FROM memories m WHERE m.deleted = 0`
	args := []any{}
	if expr != "" {
		query = `SELECT ` + cols + `
			FROM fts_memories
			JOIN memories m ON fts_memories.memory_id = m.id
			WHERE fts_memories MATCH ? AND m.deleted = 0`
		args = append(args, expr)
	}
	if cursor != "" {
		updatedAt, id, ok := decodeCursor(cursor)
		if ok {
			query += ` AND (m.updated_at < ? OR (m.updated_at = ? AND m.id < ?))`
			args = append(args, updatedAt, updatedAt, id)
		}
	}
	query += ` ORDER BY m.updated_at DESC, m.id DESC LIMIT ?`
	args = append(args, limit+1)
	return s.queryPage(ctx, query, args, limit)
}

func (s *MemoryStore) queryPage(ctx context.Context, query string, args []any, limit int) ([]*Memory, string, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("list memories: %w", err)
	}
	var out []*Memory
	for rows.Next() {
		m, err := scanMemory(rows)
		if err != nil {
			_ = rows.Close()
			return nil, "", err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, "", err
	}
	_ = rows.Close()

	next := ""
	if len(out) > limit {
		next = encodeCursor(out[limit-1])
		out = out[:limit]
	}
	for _, m := range out {
		tags, err := listTags(ctx, s.db, m.ID)
		if err != nil {
			return nil, "", err
		}
		m.Tags = tags
	}
	return out, next, nil
}

// ListVersions returns the edit history of a memory, newest first.
func (s *MemoryStore) ListVersions(ctx context.Context, memoryID string) ([]*MemoryVersion, error) {
	if _, err := s.Get(ctx, memoryID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT memory_id, version, title, body, COALESCE(document, ''), saved_at
		 FROM memory_versions WHERE memory_id = ? ORDER BY version DESC`, memoryID)
	if err != nil {
		return nil, fmt.Errorf("list memory versions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []*MemoryVersion
	for rows.Next() {
		var (
			v    MemoryVersion
			date string
		)
		if err := rows.Scan(&v.MemoryID, &v.Version, &v.Title, &v.Body, &v.Document, &date); err != nil {
			return nil, fmt.Errorf("scan memory version: %w", err)
		}
		if err := parseTime(date, &v.SavedAt); err != nil {
			return nil, err
		}
		out = append(out, &v)
	}
	return out, rows.Err()
}

// GetVersion returns a single historical version of a memory.
func (s *MemoryStore) GetVersion(ctx context.Context, memoryID string, version int) (*MemoryVersion, error) {
	var (
		v    MemoryVersion
		date string
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT memory_id, version, title, body, COALESCE(document, ''), saved_at
		 FROM memory_versions WHERE memory_id = ? AND version = ?`,
		memoryID, version).Scan(&v.MemoryID, &v.Version, &v.Title, &v.Body, &v.Document, &date)
	if err == sql.ErrNoRows {
		return nil, ErrVersionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get memory version: %w", err)
	}
	if err := parseTime(date, &v.SavedAt); err != nil {
		return nil, err
	}
	return &v, nil
}

// ListRefs returns the internal references stored for a memory.
func (s *MemoryStore) ListRefs(ctx context.Context, memoryID string) ([]markdown.Reference, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT target_type, target_id FROM memory_refs WHERE memory_id = ? ORDER BY target_type, target_id`,
		memoryID)
	if err != nil {
		return nil, fmt.Errorf("list memory refs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []markdown.Reference
	for rows.Next() {
		var (
			ref markdown.Reference
			typ string
		)
		if err := rows.Scan(&typ, &ref.ID); err != nil {
			return nil, err
		}
		ref.Type = markdown.RefType(typ)
		out = append(out, ref)
	}
	return out, rows.Err()
}

// --- shared transaction steps ---

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// lockMemory reads the memory's revision inside tx, rejecting missing or
// deleted memories and stale base revisions.
func lockMemory(ctx context.Context, tx *sql.Tx, id string, baseRevision *int) (int, error) {
	var deleted, revision int
	err := tx.QueryRowContext(ctx, `SELECT deleted, revision FROM memories WHERE id = ?`, id).
		Scan(&deleted, &revision)
	if err == sql.ErrNoRows {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("read memory: %w", err)
	}
	if deleted != 0 {
		return 0, ErrDeleted
	}
	if baseRevision != nil && *baseRevision != revision {
		return 0, &ConflictError{Current: revision}
	}
	return revision, nil
}

// versionCoalesceWindow merges rapid document saves (debounced autosave while
// typing) into the latest version so history stays useful and bounded.
const versionCoalesceWindow = 2 * time.Minute

// finalizeTx recomputes everything derived from a memory's blocks — the
// legacy body, search_text, memory_refs — bumps the revision, and records a
// version snapshot. When coalesce is true and the latest version is a recent
// document save (not the creation version), that version is overwritten
// instead of a new one being appended.
func (s *MemoryStore) finalizeTx(ctx context.Context, tx *sql.Tx, id, now string, coalesce bool) (int, error) {
	var title, description, location string
	if err := tx.QueryRowContext(ctx, `SELECT title, description, location FROM memories WHERE id = ?`, id).
		Scan(&title, &description, &location); err != nil {
		return 0, fmt.Errorf("read memory: %w", err)
	}
	blocks, err := loadBlocks(ctx, tx, id)
	if err != nil {
		return 0, err
	}
	tags, err := listTags(ctx, tx, id)
	if err != nil {
		return 0, err
	}

	body := joinMarkdown(blocks)
	search := buildSearchText(body, blocks, description, location, tags)

	var revision int
	if err := tx.QueryRowContext(ctx,
		`UPDATE memories SET body = ?, search_text = ?, revision = revision + 1, updated_at = ?
		 WHERE id = ? RETURNING revision`,
		body, search, now, id).Scan(&revision); err != nil {
		return 0, fmt.Errorf("update memory derived fields: %w", err)
	}

	if err := replaceRefsTx(ctx, tx, id, body, blocks); err != nil {
		return 0, err
	}

	snapshot, err := encodeSnapshot(blocks)
	if err != nil {
		return 0, err
	}
	if coalesce {
		var (
			version  int
			savedAt  string
			document sql.NullString
		)
		err := tx.QueryRowContext(ctx,
			`SELECT version, saved_at, document FROM memory_versions WHERE memory_id = ?
			 ORDER BY version DESC LIMIT 1`, id).Scan(&version, &savedAt, &document)
		if err != nil && err != sql.ErrNoRows {
			return 0, fmt.Errorf("read latest version: %w", err)
		}
		if err == nil && version > 1 && document.Valid {
			if t, perr := time.Parse(time.RFC3339Nano, savedAt); perr == nil && s.now().Sub(t) < versionCoalesceWindow {
				if _, err := tx.ExecContext(ctx,
					`UPDATE memory_versions SET title = ?, body = ?, document = ?, saved_at = ?
					 WHERE memory_id = ? AND version = ?`,
					title, body, snapshot, now, id, version); err != nil {
					return 0, fmt.Errorf("coalesce memory version: %w", err)
				}
				return revision, nil
			}
		}
	}
	var next int
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version), 0) + 1 FROM memory_versions WHERE memory_id = ?`, id).Scan(&next); err != nil {
		return 0, fmt.Errorf("compute next memory version: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO memory_versions (id, memory_id, version, title, body, document, saved_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		newID(), id, next, title, body, snapshot, now); err != nil {
		return 0, fmt.Errorf("insert memory version: %w", err)
	}
	return revision, nil
}

// joinMarkdown is the legacy "body": every text block's Markdown, in order.
func joinMarkdown(blocks []*Block) string {
	var parts []string
	for _, b := range blocks {
		if b.Type == BlockText && strings.TrimSpace(b.Markdown) != "" {
			parts = append(parts, b.Markdown)
		}
	}
	return strings.Join(parts, "\n\n")
}

// buildSearchText is what the FTS index sees for a memory's body column.
// Captions belong to the memory, so they are searchable here — they are never
// written to the source media's own metadata.
func buildSearchText(body string, blocks []*Block, description, location string, tags []string) string {
	parts := []string{body}
	for _, b := range blocks {
		for _, img := range b.Images {
			if img.Caption != "" {
				parts = append(parts, img.Caption)
			}
		}
	}
	for _, v := range []string{description, location, strings.Join(tags, " ")} {
		if strings.TrimSpace(v) != "" {
			parts = append(parts, v)
		}
	}
	return strings.Join(parts, "\n")
}

// replaceRefsTx rewrites memory_refs from the Markdown links in text blocks
// plus one media reference per distinct image source.
func replaceRefsTx(ctx context.Context, tx *sql.Tx, id, body string, blocks []*Block) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM memory_refs WHERE memory_id = ?`, id); err != nil {
		return fmt.Errorf("clear memory refs: %w", err)
	}
	refs := markdown.ParseReferences(body)
	for _, b := range blocks {
		for _, img := range b.Images {
			refs = append(refs, markdown.Reference{Type: markdown.RefMedia, ID: img.SourceFileID})
		}
	}
	seen := make(map[[2]string]struct{}, len(refs))
	for _, r := range refs {
		key := [2]string{string(r.Type), r.ID}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO memory_refs (memory_id, target_type, target_id) VALUES (?, ?, ?)`,
			id, string(r.Type), r.ID); err != nil {
			return fmt.Errorf("insert memory ref: %w", err)
		}
	}
	return nil
}

// --- tags ---

func listTags(ctx context.Context, q querier, memoryID string) ([]string, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT t.name FROM memory_tags mt JOIN tags t ON t.id = mt.tag_id
		 WHERE mt.memory_id = ? ORDER BY t.name COLLATE NOCASE`, memoryID)
	if err != nil {
		return nil, fmt.Errorf("list memory tags: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// replaceTagsTx sets a memory's tags by name, creating library tags that do
// not exist yet (the same tag namespace files use).
func replaceTagsTx(ctx context.Context, tx *sql.Tx, memoryID string, names []string, now string) error {
	if len(names) > 100 {
		return &ValidationError{msg: "too many tags (max 100)"}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM memory_tags WHERE memory_id = ?`, memoryID); err != nil {
		return fmt.Errorf("clear memory tags: %w", err)
	}
	seen := map[string]struct{}{}
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		if len(name) > 100 {
			return &ValidationError{msg: "tag names are limited to 100 characters"}
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		var tagID string
		err := tx.QueryRowContext(ctx, `SELECT id FROM tags WHERE name = ? COLLATE NOCASE`, name).Scan(&tagID)
		if err == sql.ErrNoRows {
			tagID = newID()
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO tags (id, name, created_at) VALUES (?, ?, ?)`, tagID, name, now); err != nil {
				return fmt.Errorf("create tag: %w", err)
			}
		} else if err != nil {
			return fmt.Errorf("find tag: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO memory_tags (memory_id, tag_id, created_at) VALUES (?, ?, ?)`,
			memoryID, tagID, now); err != nil {
			return fmt.Errorf("attach memory tag: %w", err)
		}
	}
	return nil
}

// --- cursors ---

// encodeCursor flattens a (updatedAt, id) keyset value into an opaque string.
func encodeCursor(m *Memory) string {
	raw := rfc3339(m.UpdatedAt) + "\x01" + m.ID
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// decodeCursor reverses encodeCursor.
func decodeCursor(cursor string) (updatedAt, id string, ok bool) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return "", "", false
	}
	sep := strings.IndexByte(string(raw), '\x01')
	if sep <= 0 || sep == len(raw)-1 {
		return "", "", false
	}
	return string(raw[:sep]), string(raw[sep+1:]), true
}

// --- scanners & helpers ---

type rowScanner interface {
	Scan(dest ...any) error
}

func scanMemory(row rowScanner) (*Memory, error) {
	var (
		m          Memory
		cover      sql.NullString
		date       sql.NullString
		deleted    int
		createdStr string
		updatedStr string
	)
	err := row.Scan(&m.ID, &m.Title, &m.Body, &m.Description, &m.Location, &cover, &date,
		&m.Revision, &deleted, &createdStr, &updatedStr)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan memory: %w", err)
	}
	m.Deleted = deleted != 0
	m.CoverFileID = cover.String
	m.MemoryDate = optionalDate(date)
	if err := parseTime(createdStr, &m.CreatedAt); err != nil {
		return nil, err
	}
	if err := parseTime(updatedStr, &m.UpdatedAt); err != nil {
		return nil, err
	}
	return &m, nil
}

func prefixed(prefix, cols string) string {
	parts := strings.Split(cols, ",")
	for i, p := range parts {
		parts[i] = prefix + strings.TrimSpace(p)
	}
	return strings.Join(parts, ", ")
}

func clampLimit(limit int) int {
	if limit <= 0 {
		return 50
	}
	if limit > 200 {
		return 200
	}
	return limit
}

func optionalDate(s sql.NullString) *time.Time {
	if !s.Valid || s.String == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, s.String)
	if err != nil {
		return nil
	}
	return &t
}

func optionalTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return (*t).UTC().Format(time.RFC3339Nano)
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
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
