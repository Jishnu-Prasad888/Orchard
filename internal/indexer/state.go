package indexer

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// StateStore persists and queries indexed file records in the per-library
// SQLite database. All times are stored as RFC3339Nano UTC strings.
type StateStore struct {
	db *sql.DB
}

// NewStateStore wraps a per-library database connection.
func NewStateStore(db *sql.DB) *StateStore {
	return &StateStore{db: db}
}

// Upsert inserts or updates an indexed file record. It uses INSERT OR REPLACE
// so that a re-scan that finds the same file simply refreshes last_seen_at and
// status without changing first_seen_at.
func (s *StateStore) Upsert(ctx context.Context, f *IndexedFile) error {
	const query = `
		INSERT INTO indexed_files
			(id, rel_path, size_bytes, mod_time, content_hash, status,
			 first_seen_at, last_seen_at, indexed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(rel_path) DO UPDATE SET
			size_bytes    = excluded.size_bytes,
			mod_time      = excluded.mod_time,
			content_hash  = excluded.content_hash,
			status        = excluded.status,
			last_seen_at  = excluded.last_seen_at,
			indexed_at    = excluded.indexed_at`
	_, err := s.db.ExecContext(ctx, query,
		f.ID, f.RelPath, f.SizeBytes, rfc3339(f.ModTime),
		nullString(f.ContentHash), string(f.Status),
		rfc3339(f.FirstSeenAt), rfc3339(f.LastSeenAt), rfc3339(f.IndexedAt),
	)
	if err != nil {
		return fmt.Errorf("upsert indexed file %q: %w", f.RelPath, err)
	}
	return nil
}

// GetByPath returns the indexed file record for the given relative path, or
// (nil, nil) when not found.
func (s *StateStore) GetByPath(ctx context.Context, relPath string) (*IndexedFile, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, rel_path, size_bytes, mod_time, content_hash, status,
		        first_seen_at, last_seen_at, indexed_at
		 FROM indexed_files WHERE rel_path = ?`, relPath)
	return scanFile(row)
}

// GetByHash returns all indexed file records matching the given content hash.
// Used for move detection: when a new path has the same hash as a missing
// entry, the file has moved rather than been deleted and re-created.
func (s *StateStore) GetByHash(ctx context.Context, hash string) ([]*IndexedFile, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, rel_path, size_bytes, mod_time, content_hash, status,
		        first_seen_at, last_seen_at, indexed_at
		 FROM indexed_files WHERE content_hash = ?`, hash)
	if err != nil {
		return nil, fmt.Errorf("query by hash: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanFiles(rows)
}

// AllPresent returns all files currently marked as present or missing. This is
// the set the scanner reconciles at the end of each walk.
func (s *StateStore) AllPresent(ctx context.Context) ([]*IndexedFile, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, rel_path, size_bytes, mod_time, content_hash, status,
		        first_seen_at, last_seen_at, indexed_at
		 FROM indexed_files WHERE status IN ('present', 'missing')`)
	if err != nil {
		return nil, fmt.Errorf("query all present files: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanFiles(rows)
}

// MarkMissing marks indexed files that were not observed during a scan as
// missing. seenIDs is the set of file IDs touched in this scan run.
func (s *StateStore) MarkMissing(ctx context.Context, seenIDs map[string]struct{}, now time.Time) (int, error) {
	// Fetch all present files and mark the ones not in seenIDs.
	files, err := s.AllPresent(ctx)
	if err != nil {
		return 0, err
	}
	var count int
	for _, f := range files {
		if _, seen := seenIDs[f.ID]; seen {
			continue
		}
		if f.Status == StatusMissing {
			continue // already missing; leave last_seen_at intact
		}
		f.Status = StatusMissing
		f.LastSeenAt = now
		f.IndexedAt = now
		if err := s.Upsert(ctx, f); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

// UpdatePath updates the relative path of an existing record. Used when a
// move is detected: the old entry's path is replaced with the new location.
func (s *StateStore) UpdatePath(ctx context.Context, id, newRelPath string, now time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE indexed_files SET rel_path = ?, last_seen_at = ?, indexed_at = ?,
		                          status = 'present'
		 WHERE id = ?`,
		newRelPath, rfc3339(now), rfc3339(now), id)
	if err != nil {
		return fmt.Errorf("update path for %s: %w", id, err)
	}
	return nil
}

// Counts returns a summary of the current index state.
func (s *StateStore) Counts(ctx context.Context) (present, missing, deleted int, err error) {
	rows, queryErr := s.db.QueryContext(ctx,
		`SELECT status, COUNT(*) FROM indexed_files GROUP BY status`)
	if queryErr != nil {
		return 0, 0, 0, fmt.Errorf("count indexed files: %w", queryErr)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var status string
		var n int
		if scanErr := rows.Scan(&status, &n); scanErr != nil {
			return 0, 0, 0, scanErr
		}
		switch Status(status) {
		case StatusPresent:
			present = n
		case StatusMissing:
			missing = n
		case StatusDeleted:
			deleted = n
		}
	}
	return present, missing, deleted, rows.Err()
}

// --- row scanning helpers ---

type rowScanner interface {
	Scan(dest ...any) error
}

func scanFile(row rowScanner) (*IndexedFile, error) {
	var (
		f           IndexedFile
		modTimeStr  string
		contentHash sql.NullString
		firstStr    string
		lastStr     string
		indexedStr  string
		status      string
	)
	err := row.Scan(&f.ID, &f.RelPath, &f.SizeBytes, &modTimeStr, &contentHash,
		&status, &firstStr, &lastStr, &indexedStr)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("scan indexed file: %w", err)
	}
	f.Status = Status(status)
	f.ContentHash = contentHash.String
	if err := parseTime(modTimeStr, &f.ModTime); err != nil {
		return nil, err
	}
	if err := parseTime(firstStr, &f.FirstSeenAt); err != nil {
		return nil, err
	}
	if err := parseTime(lastStr, &f.LastSeenAt); err != nil {
		return nil, err
	}
	if err := parseTime(indexedStr, &f.IndexedAt); err != nil {
		return nil, err
	}
	return &f, nil
}

func scanFiles(rows *sql.Rows) ([]*IndexedFile, error) {
	var out []*IndexedFile
	for rows.Next() {
		f, err := scanFile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
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

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
