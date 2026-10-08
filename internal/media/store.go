package media

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/Jishnu-Prasad888/Cairn/internal/likeutil"
)

// FileStore is the read/write repository for files and folders in a per-library
// SQLite database. All methods operate on relative paths.
type FileStore struct {
	db        *sql.DB
	libraryID string
}

// NewFileStore wraps a per-library database pool.
func NewFileStore(db *sql.DB, libraryID string) *FileStore {
	return &FileStore{db: db, libraryID: libraryID}
}

// --- listing ---

// List returns a paginated, filtered, sorted page of files.
func (s *FileStore) List(ctx context.Context, opts ListOptions) (*Page, error) {
	opts.Defaults()

	// Build the WHERE clause.
	var conditions []string
	var args []any

	// Status filter.
	conditions = append(conditions, "status = ?")
	args = append(args, string(opts.Status))

	// Folder filter.
	if opts.FolderPath != "" && opts.FolderPath != "." {
		folderPath := filepath.ToSlash(opts.FolderPath)
		esc := likeutil.Escape(folderPath) + "/%"
		if opts.Recursive {
			conditions = append(conditions, "rel_path LIKE ? "+likeutil.EscapeClause)
			args = append(args, esc)
		} else {
			// Direct children only: folder/name (no further slash after folder/).
			conditions = append(conditions,
				"rel_path LIKE ? "+likeutil.EscapeClause+" AND rel_path NOT LIKE ? "+likeutil.EscapeClause)
			args = append(args, esc, likeutil.Escape(folderPath)+"/%/%")
		}
	} else if !opts.Recursive && (opts.FolderPath == "" || opts.FolderPath == ".") {
		// Root-level files only (no slash in rel_path).
		conditions = append(conditions, "rel_path NOT LIKE '%/%'")
	}

	// Media type filter. The type is stored on the row by the indexer rather
	// than read from media_metadata, because only the photo extractor ever
	// writes to that table: filtering on it returned nothing for videos and
	// everything for photos, so the Videos page showed the photo library.
	if opts.Type != "" {
		conditions = append(conditions, "media_type = ?")
		args = append(args, string(opts.Type))
	}

	// Cursor: rel_path > cursor for forward pagination.
	if opts.Cursor != "" {
		switch opts.Sort {
		case SortBySize:
			conditions = append(conditions, "size_bytes > (SELECT size_bytes FROM indexed_files WHERE rel_path = ?)")
			args = append(args, opts.Cursor)
		case SortByModTime:
			conditions = append(conditions, "mod_time > (SELECT mod_time FROM indexed_files WHERE rel_path = ?)")
			args = append(args, opts.Cursor)
		default: // SortByName
			conditions = append(conditions, "rel_path > ?")
			args = append(args, opts.Cursor)
		}
	}

	where := ""
	if len(conditions) > 0 {
		where = "WHERE " + strings.Join(conditions, " AND ")
	}

	// ORDER BY clause.
	orderCol := "rel_path"
	switch opts.Sort {
	case SortBySize:
		orderCol = "size_bytes"
	case SortByModTime:
		orderCol = "mod_time"
	}
	dir := "ASC"
	if opts.Order == SortDesc {
		dir = "DESC"
	}

	query := fmt.Sprintf(`
		SELECT id, rel_path, size_bytes, mod_time, content_hash, media_type, status,
		       first_seen_at, last_seen_at
		FROM indexed_files
		%s
		ORDER BY %s %s, rel_path ASC
		LIMIT ?`, where, orderCol, dir)
	args = append(args, opts.Limit+1) // fetch one extra to detect next page

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list files: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var files []*File
	for rows.Next() {
		f, err := s.scanFile(rows)
		if err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	page := &Page{}
	if len(files) > opts.Limit {
		page.NextCursor = files[opts.Limit-1].RelPath
		files = files[:opts.Limit]
	}
	page.Files = files

	// Approximate total (without the cursor constraint).
	total, err := s.countFiles(ctx, opts)
	if err == nil {
		page.Total = total
	}
	return page, nil
}

// countFiles returns an approximate total for the given filter options.
func (s *FileStore) countFiles(ctx context.Context, opts ListOptions) (int, error) {
	var conditions []string
	var args []any
	conditions = append(conditions, "status = ?")
	args = append(args, string(opts.Status))
	if opts.FolderPath != "" && opts.FolderPath != "." {
		folderPath := filepath.ToSlash(opts.FolderPath)
		if opts.Recursive {
			conditions = append(conditions, "rel_path LIKE ? "+likeutil.EscapeClause)
			args = append(args, likeutil.Escape(folderPath)+"/%")
		} else {
			conditions = append(conditions,
				"rel_path LIKE ? "+likeutil.EscapeClause+" AND rel_path NOT LIKE ? "+likeutil.EscapeClause)
			args = append(args, likeutil.Escape(folderPath)+"/%", likeutil.Escape(folderPath)+"/%/%")
		}
	} else if !opts.Recursive {
		conditions = append(conditions, "rel_path NOT LIKE '%/%'")
	}
	// The type filter has to be applied here too, or the count promises a page
	// of photos the query will never return.
	if opts.Type != "" {
		conditions = append(conditions, "media_type = ?")
		args = append(args, string(opts.Type))
	}
	where := "WHERE " + strings.Join(conditions, " AND ")
	var n int
	err := s.db.QueryRowContext(ctx,
		fmt.Sprintf("SELECT COUNT(*) FROM indexed_files %s", where), args...).Scan(&n)
	return n, err
}

// GetByID returns a single file by its ID.
func (s *FileStore) GetByID(ctx context.Context, id string) (*File, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, rel_path, size_bytes, mod_time, content_hash, media_type, status,
		        first_seen_at, last_seen_at
		 FROM indexed_files WHERE id = ?`, id)
	f, err := s.scanFile(row)
	if err != nil {
		return nil, err
	}
	if f == nil {
		return nil, ErrNotFound
	}
	return f, nil
}

// GetByRelPath returns a single file by its relative path.
func (s *FileStore) GetByRelPath(ctx context.Context, relPath string) (*File, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, rel_path, size_bytes, mod_time, content_hash, media_type, status,
		        first_seen_at, last_seen_at
		 FROM indexed_files WHERE rel_path = ?`, relPath)
	f, err := s.scanFile(row)
	if err != nil {
		return nil, err
	}
	if f == nil {
		return nil, ErrNotFound
	}
	return f, nil
}

// UpsertFromPath inserts or updates the indexed_files row for a given
// relative path after an upload or file operation. The media package does not
// run the full scanner; it just keeps the index in sync with explicit ops.
func (s *FileStore) UpsertFromPath(ctx context.Context, relPath string, sizeBytes int64, modTime time.Time) error {
	now := rfc3339(time.Now().UTC())
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO indexed_files
			(id, rel_path, size_bytes, mod_time, content_hash, media_type, status,
			 first_seen_at, last_seen_at, indexed_at)
		VALUES (?, ?, ?, ?, NULL, ?, 'present', ?, ?, ?)
		ON CONFLICT(rel_path) DO UPDATE SET
			size_bytes   = excluded.size_bytes,
			mod_time     = excluded.mod_time,
			content_hash = NULL,
			media_type   = excluded.media_type,
			status       = 'present',
			last_seen_at = excluded.last_seen_at,
			indexed_at   = excluded.indexed_at`,
		newID(), relPath, sizeBytes, rfc3339(modTime), string(DetectMediaType(relPath)), now, now, now)
	return err
}

// DeleteRow removes an indexed_files row entirely (used for permanent deletes
// after the trash entry is also removed).
func (s *FileStore) DeleteRow(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM indexed_files WHERE id = ?`, id)
	return err
}

// MarkDeleted sets status='deleted' on an indexed_files row.
func (s *FileStore) MarkDeleted(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE indexed_files SET status = 'deleted', last_seen_at = ?, indexed_at = ?
		 WHERE id = ?`, rfc3339(time.Now().UTC()), rfc3339(time.Now().UTC()), id)
	return err
}

// MarkPresent sets status='present' and updates the rel_path (for restore).
func (s *FileStore) MarkPresent(ctx context.Context, id, relPath string) error {
	now := rfc3339(time.Now().UTC())
	_, err := s.db.ExecContext(ctx,
		`UPDATE indexed_files
		 SET status = 'present', rel_path = ?, media_type = ?, last_seen_at = ?, indexed_at = ?
		 WHERE id = ?`, relPath, string(DetectMediaType(relPath)), now, now, id)
	return err
}

// UpdateRelPath updates the relative path of a file (rename/move operation).
//
// media_type is re-derived because a rename can change the extension: moving
// holiday.jpg to holiday.mp4 has to change which pages the file appears on.
func (s *FileStore) UpdateRelPath(ctx context.Context, id, newRelPath string) error {
	now := rfc3339(time.Now().UTC())
	_, err := s.db.ExecContext(ctx,
		`UPDATE indexed_files SET rel_path = ?, media_type = ?, last_seen_at = ?, indexed_at = ?
		 WHERE id = ?`, newRelPath, string(DetectMediaType(newRelPath)), now, now, id)
	return err
}

// --- trash ---

// AddToTrash records a trash entry for a soft-deleted file.
func (s *FileStore) AddToTrash(ctx context.Context, entry *TrashEntry) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO trash (file_id, original_path, trash_path, deleted_at, deleted_by)
		 VALUES (?, ?, ?, ?, ?)`,
		entry.FileID, entry.OriginalPath, entry.TrashPath,
		rfc3339(entry.DeletedAt), nullStr(entry.DeletedBy))
	return err
}

// GetTrashEntry returns the trash record for a file.
func (s *FileStore) GetTrashEntry(ctx context.Context, fileID string) (*TrashEntry, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT file_id, original_path, trash_path, deleted_at, deleted_by
		 FROM trash WHERE file_id = ?`, fileID)
	var e TrashEntry
	var deletedBy sql.NullString
	var deletedAtStr string
	err := row.Scan(&e.FileID, &e.OriginalPath, &e.TrashPath, &deletedAtStr, &deletedBy)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	e.DeletedBy = deletedBy.String
	if err := parseTime(deletedAtStr, &e.DeletedAt); err != nil {
		return nil, err
	}
	return &e, nil
}

// RemoveTrashEntry deletes the trash record (used after restore or permanent delete).
func (s *FileStore) RemoveTrashEntry(ctx context.Context, fileID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM trash WHERE file_id = ?`, fileID)
	return err
}

// ListTrash returns all files currently in trash.
func (s *FileStore) ListTrash(ctx context.Context) ([]*File, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT f.id, f.rel_path, f.size_bytes, f.mod_time, f.content_hash, f.media_type, f.status,
		        f.first_seen_at, f.last_seen_at
		 FROM indexed_files f
		 JOIN trash t ON t.file_id = f.id
		 ORDER BY t.deleted_at DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return s.scanFiles(rows)
}

// --- folders ---

// UpsertFolder inserts or updates a folder record.
func (s *FileStore) UpsertFolder(ctx context.Context, f *Folder) error {
	now := rfc3339(time.Now().UTC())
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO folders (id, rel_path, parent_id, name, file_count, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(rel_path) DO UPDATE SET
			parent_id  = excluded.parent_id,
			name       = excluded.name,
			file_count = excluded.file_count,
			updated_at = excluded.updated_at`,
		f.ID, f.RelPath, nullStr(f.ParentID), f.Name, f.FileCount, now, now)
	return err
}

// GetFolderByPath returns a folder by its relative path.
func (s *FileStore) GetFolderByPath(ctx context.Context, relPath string) (*Folder, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, rel_path, parent_id, name, file_count, created_at, updated_at
		 FROM folders WHERE rel_path = ?`, relPath)
	return s.scanFolder(row)
}

// CountPresentUnder returns how many present files live in folderPath or any
// folder beneath it. It is computed from the index on demand because the
// folders table is not maintained by the indexer.
func (s *FileStore) CountPresentUnder(ctx context.Context, folderPath string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM indexed_files WHERE status = ? AND rel_path LIKE ? `+likeutil.EscapeClause,
		string(FileStatusPresent), likeutil.Escape(filepath.ToSlash(folderPath))+"/%").Scan(&n)
	return n, err
}

// CountsByType returns the number of present files per media type in a single
// indexed pass, so dashboards need one query instead of one per type.
func (s *FileStore) CountsByType(ctx context.Context) (map[MediaType]int, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT media_type, COUNT(*) FROM indexed_files WHERE status = ? GROUP BY media_type`,
		string(FileStatusPresent))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[MediaType]int{}
	for rows.Next() {
		var t string
		var n int
		if err := rows.Scan(&t, &n); err != nil {
			return nil, err
		}
		out[MediaType(t)] = n
	}
	return out, rows.Err()
}

// CollectionCounts holds the sizes of the user-curated collections.
type CollectionCounts struct {
	Albums    int `json:"albums"`
	Tags      int `json:"tags"`
	Favorites int `json:"favorites"`
}

// CountCollections returns album, tag and favorite totals in one round trip.
// Favorites only count files that are still present, matching the favorites list.
func (s *FileStore) CountCollections(ctx context.Context) (CollectionCounts, error) {
	var c CollectionCounts
	err := s.db.QueryRowContext(ctx,
		`SELECT (SELECT COUNT(*) FROM albums),
		        (SELECT COUNT(*) FROM tags),
		        (SELECT COUNT(*) FROM favorites fav
		           JOIN indexed_files f ON f.id = fav.file_id WHERE f.status = 'present')`,
	).Scan(&c.Albums, &c.Tags, &c.Favorites)
	return c, err
}

// ListFolders returns direct children of a parent folder path.
func (s *FileStore) ListFolders(ctx context.Context, parentPath string) ([]*Folder, error) {
	var rows *sql.Rows
	var err error
	if parentPath == "" || parentPath == "." {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, rel_path, parent_id, name, file_count, created_at, updated_at
			 FROM folders WHERE parent_id IS NULL ORDER BY name COLLATE NOCASE`)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT f.id, f.rel_path, f.parent_id, f.name, f.file_count, f.created_at, f.updated_at
			 FROM folders f JOIN folders p ON f.parent_id = p.id
			 WHERE p.rel_path = ?
			 ORDER BY f.name COLLATE NOCASE`, parentPath)
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return s.scanFolders(rows)
}

// --- duplicates ---

// ListDuplicates returns groups of present files that share a content hash
// (identical bytes), ordered by content hash. Cursor pagination is by the last
// seen content hash so page boundaries never split a group. The per-hash
// member lookups hit the partial index on content_hash, so each page only
// touches the groups it returns.
func (s *FileStore) ListDuplicates(ctx context.Context, opts DuplicateOptions) (*DuplicatesPage, error) {
	opts.Defaults()

	page := &DuplicatesPage{}

	// Total number of duplicate groups (distinct hashes with >1 present file).
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM (
			SELECT content_hash FROM indexed_files
			WHERE status = 'present' AND content_hash IS NOT NULL
			GROUP BY content_hash
			HAVING COUNT(*) > 1
		)`).Scan(&page.Total)
	if err != nil {
		return nil, fmt.Errorf("count duplicate groups: %w", err)
	}

	// Next page of duplicate hashes. Fetch one extra to detect the next page.
	hashQuery := `
		SELECT content_hash FROM indexed_files
		WHERE status = 'present' AND content_hash IS NOT NULL
		AND content_hash > ?
		GROUP BY content_hash
		HAVING COUNT(*) > 1
		ORDER BY content_hash
		LIMIT ?`
	args := []any{opts.Cursor, opts.Limit + 1}
	if opts.Cursor == "" {
		// No cursor: hashes are non-null so any value sorts above empty;
		// "content_hash > ''" is a no-op for non-null rows.
		args[0] = ""
	}

	rows, err := s.db.QueryContext(ctx, hashQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("find duplicate hashes: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var hashes []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, err
		}
		hashes = append(hashes, h)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if len(hashes) > opts.Limit {
		page.NextCursor = hashes[opts.Limit-1]
		hashes = hashes[:opts.Limit]
	}

	for _, h := range hashes {
		members, err := s.queryFiles(ctx,
			`WHERE status = 'present' AND content_hash = ? ORDER BY rel_path`, h)
		if err != nil {
			return nil, err
		}
		if len(members) < 2 {
			continue // hash group shrank since the page query; keep pages consistent
		}
		page.Groups = append(page.Groups, &DuplicateGroup{
			ContentHash: h,
			SizeBytes:   members[0].SizeBytes,
			Files:       members,
		})
	}

	return page, nil
}

// queryFiles runs a file-select query whose WHERE clause is appended to the
// canonical column list, returning scanned files.
func (s *FileStore) queryFiles(ctx context.Context, whereTail string, args ...any) ([]*File, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, rel_path, size_bytes, mod_time, content_hash, media_type, status,
		        first_seen_at, last_seen_at
		 FROM indexed_files `+whereTail, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return s.scanFiles(rows)
}

// --- row scanners ---

type rowScanner interface {
	Scan(dest ...any) error
}

func (s *FileStore) scanFile(row rowScanner) (*File, error) {
	var (
		f         File
		modStr    string
		firstStr  string
		lastStr   string
		hash      sql.NullString
		mediaType string
		status    string
	)
	err := row.Scan(&f.ID, &f.RelPath, &f.SizeBytes, &modStr, &hash, &mediaType, &status, &firstStr, &lastStr)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan file: %w", err)
	}
	f.LibraryID = s.libraryID
	f.ContentHash = hash.String
	f.Status = FileStatus(status)
	f.Name = filepath.Base(f.RelPath)
	f.FolderPath = filepath.ToSlash(filepath.Dir(f.RelPath))
	if f.FolderPath == "." {
		f.FolderPath = ""
	}
	f.MediaType = MediaType(mediaType)
	f.MIMEType = DetectMIME(f.RelPath)
	if err := parseTime(modStr, &f.ModTime); err != nil {
		return nil, err
	}
	if err := parseTime(firstStr, &f.FirstSeenAt); err != nil {
		return nil, err
	}
	if err := parseTime(lastStr, &f.LastSeenAt); err != nil {
		return nil, err
	}
	return &f, nil
}

func (s *FileStore) scanFiles(rows *sql.Rows) ([]*File, error) {
	var out []*File
	for rows.Next() {
		f, err := s.scanFile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *FileStore) scanFolder(row rowScanner) (*Folder, error) {
	var (
		f          Folder
		parentID   sql.NullString
		createdStr string
		updatedStr string
	)
	err := row.Scan(&f.ID, &f.RelPath, &parentID, &f.Name, &f.FileCount, &createdStr, &updatedStr)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan folder: %w", err)
	}
	f.LibraryID = s.libraryID
	f.ParentID = parentID.String
	if err := parseTime(createdStr, &f.CreatedAt); err != nil {
		return nil, err
	}
	if err := parseTime(updatedStr, &f.UpdatedAt); err != nil {
		return nil, err
	}
	return &f, nil
}

func (s *FileStore) scanFolders(rows *sql.Rows) ([]*Folder, error) {
	var out []*Folder
	for rows.Next() {
		f, err := s.scanFolder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// --- helpers ---

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
