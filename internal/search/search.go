// Package search provides full-text search over indexed files in a per-library
// SQLite database using the FTS5 virtual table.
//
// The FTS5 table (fts_files) indexes rel_path. The default unicode61 tokenizer
// splits on path separators and punctuation, so searching "beach" will match
// "holiday/beach.jpg". Triggers on indexed_files keep fts_files in sync.
package search

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Jishnu-Prasad888/Cairn/internal/fts"
	"github.com/Jishnu-Prasad888/Cairn/internal/likeutil"
	"github.com/Jishnu-Prasad888/Cairn/internal/media"
)

// ErrInvalidQuery is returned when a search query cannot be expressed safely
// (malformed FTS5 syntax, unbalanced delimiters, impossible size ranges). It
// maps to HTTP 400.
var ErrInvalidQuery = errors.New("invalid search query")

// SearchQuery specifies the parameters for a full-text file search.
type SearchQuery struct {
	// Text is the search expression. See fts.BuildExpression for the accepted
	// syntax. Empty means match all present files.
	Text string

	// Type restricts results to a specific media type. Empty means all.
	Type media.MediaType

	// FolderPath restricts results to files under this folder (prefix match).
	// Empty means search the entire library.
	FolderPath string

	// Tag restricts results to files carrying a tag with this name (case-
	// insensitive). Empty means all.
	Tag string

	// AlbumID restricts results to files in this album. Empty means all.
	AlbumID string

	// PersonID restricts results to files containing a face assigned to this
	// person. Empty means all.
	PersonID string

	// MinSize and MaxSize restrict results by file size in bytes. Zero values
	// are ignored.
	MinSize int64
	MaxSize int64

	// DateFrom and DateTo restrict results by indexed_files.mod_time.
	// Zero values are ignored.
	DateFrom time.Time
	DateTo   time.Time

	// Cursor is the last result seen, encoded by NextCursor. Used for keyset
	// pagination in both the FTS and browse paths.
	Cursor string

	// Limit is the maximum number of results to return. Defaults to 50.
	Limit int
}

// Defaults fills in zero values with sensible defaults.
func (q *SearchQuery) Defaults() {
	if q.Limit <= 0 {
		q.Limit = 50
	}
	if q.Limit > 200 {
		q.Limit = 200
	}
}

// SearchResult is a single file matching the search query.
type SearchResult struct {
	File *media.File
}

// SearchPage is a paginated list of search results.
type SearchPage struct {
	Results    []*SearchResult
	NextCursor string
	Total      int
}

// SearchStore runs FTS5 queries against a per-library database.
type SearchStore struct {
	db        *sql.DB
	libraryID string
}

// NewSearchStore wraps a per-library database pool.
func NewSearchStore(db *sql.DB, libraryID string) *SearchStore {
	return &SearchStore{db: db, libraryID: libraryID}
}

// Search runs a full-text search and returns a page of results.
func (s *SearchStore) Search(ctx context.Context, q SearchQuery) (*SearchPage, error) {
	q.Defaults()

	if q.MinSize > 0 && q.MaxSize > 0 && q.MinSize > q.MaxSize {
		return nil, fmt.Errorf("%w: min_size greater than max_size", ErrInvalidQuery)
	}

	// Extra filters applied after the primary WHERE clause.
	extraConds, extraArgs := buildExtraFilters(q)

	var (
		queryStr  string
		queryArgs []any
		ranks     []float64
	)

	if q.Text != "" {
		ftsExpr, err := fts.BuildExpression(q.Text)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidQuery, err)
		}
		if ftsExpr == "" {
			// The query produced nothing searchable (e.g. punctuation only).
			return &SearchPage{}, nil
		}

		rankCond, rankArgs := rankCursorCond(q.Cursor)
		whereClause := "WHERE f.status = 'present'"
		if len(extraConds) > 0 {
			whereClause += " AND " + strings.Join(extraConds, " AND ")
		}
		if rankCond != "" {
			whereClause += " AND " + rankCond
		}
		queryStr = fmt.Sprintf(`
			SELECT f.id, f.rel_path, f.size_bytes, f.mod_time, f.content_hash,
			       f.media_type, f.status, f.first_seen_at, f.last_seen_at, m.rank
			FROM (SELECT file_id, rank FROM fts_files WHERE fts_files MATCH ?) AS m
			JOIN indexed_files f ON f.id = m.file_id
			%s
			ORDER BY m.rank, f.id
			LIMIT ?`, whereClause)
		queryArgs = append([]any{ftsExpr}, extraArgs...)
		queryArgs = append(queryArgs, rankArgs...)
		queryArgs = append(queryArgs, q.Limit+1)
	} else {
		whereClause := "WHERE f.status = 'present'"
		if len(extraConds) > 0 {
			whereClause += " AND " + strings.Join(extraConds, " AND ")
		}
		// Add cursor for browse pagination (keyset by id).
		if q.Cursor != "" {
			pid := q.Cursor
			if _, parsed, ok := decodeCursor(q.Cursor); ok {
				pid = parsed
			}
			whereClause += " AND f.id > ?"
			extraArgs = append(extraArgs, pid)
		}
		queryStr = fmt.Sprintf(`
			SELECT f.id, f.rel_path, f.size_bytes, f.mod_time, f.content_hash,
			       f.media_type, f.status, f.first_seen_at, f.last_seen_at, NULL
			FROM indexed_files f
			%s
			ORDER BY f.id
			LIMIT ?`, whereClause)
		queryArgs = append(extraArgs, q.Limit+1)
	}

	rows, err := s.db.QueryContext(ctx, queryStr, queryArgs...)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidQuery, err)
	}
	defer func() { _ = rows.Close() }()

	var results []*SearchResult
	for rows.Next() {
		f, rank, err := scanSearchFile(rows, s.libraryID)
		if err != nil {
			return nil, err
		}
		results = append(results, &SearchResult{File: f})
		ranks = append(ranks, rank)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	page := &SearchPage{}
	if len(results) > q.Limit {
		page.NextCursor = encodeCursor(ranks[q.Limit-1], results[q.Limit-1].File.ID)
		results = results[:q.Limit]
	}
	page.Results = results

	// Approximate total count (best-effort; ignored on error).
	total, err := s.countSearch(ctx, q)
	if err == nil {
		page.Total = total
	}
	return page, nil
}

// buildExtraFilters returns WHERE fragment conditions and bind args for the
// non-status, non-FTS filters (folder, type, tag, album, size, date range).
func buildExtraFilters(q SearchQuery) ([]string, []any) {
	var conditions []string
	var args []any

	if q.FolderPath != "" && q.FolderPath != "." {
		fp := filepath.ToSlash(q.FolderPath)
		conditions = append(conditions, "f.rel_path LIKE ? "+likeutil.EscapeClause)
		args = append(args, likeutil.Escape(fp)+"/%")
	}
	// The type lives on indexed_files, set by the indexer. media_metadata is
	// only ever written by the photo extractor, so filtering on it here matched
	// every photo and no videos at all.
	if q.Type != "" {
		conditions = append(conditions, "f.media_type = ?")
		args = append(args, string(q.Type))
	}
	if q.Tag != "" {
		conditions = append(conditions, `f.id IN (
			SELECT ft.file_id FROM file_tags ft
			JOIN tags tg ON tg.id = ft.tag_id
			WHERE tg.name = ? COLLATE NOCASE)`)
		args = append(args, q.Tag)
	}
	if q.AlbumID != "" {
		conditions = append(conditions, "f.id IN (SELECT file_id FROM album_files WHERE album_id = ?)")
		args = append(args, q.AlbumID)
	}
	if q.PersonID != "" {
		conditions = append(conditions, `f.id IN (
			SELECT fa.file_id FROM person_faces pf
			JOIN faces fa ON fa.id = pf.face_id
			WHERE pf.person_id = ?)`)
		args = append(args, q.PersonID)
	}
	if q.MinSize > 0 {
		conditions = append(conditions, "f.size_bytes >= ?")
		args = append(args, q.MinSize)
	}
	if q.MaxSize > 0 {
		conditions = append(conditions, "f.size_bytes <= ?")
		args = append(args, q.MaxSize)
	}
	if !q.DateFrom.IsZero() {
		conditions = append(conditions, "f.mod_time >= ?")
		args = append(args, q.DateFrom.UTC().Format(time.RFC3339Nano))
	}
	if !q.DateTo.IsZero() {
		conditions = append(conditions, "f.mod_time <= ?")
		args = append(args, q.DateTo.UTC().Format(time.RFC3339Nano))
	}
	return conditions, args
}

// countSearch returns an approximate total count for the given query (no cursor).
func (s *SearchStore) countSearch(ctx context.Context, q SearchQuery) (int, error) {
	extraConds, extraArgs := buildExtraFilters(q)

	var queryStr string
	var queryArgs []any

	if q.Text != "" {
		ftsExpr, err := fts.BuildExpression(q.Text)
		if err != nil {
			return 0, fmt.Errorf("%w: %v", ErrInvalidQuery, err)
		}
		if ftsExpr == "" {
			return 0, nil
		}
		whereClause := "WHERE f.status = 'present'"
		if len(extraConds) > 0 {
			whereClause += " AND " + strings.Join(extraConds, " AND ")
		}
		queryStr = fmt.Sprintf(`
			SELECT COUNT(*)
			FROM (SELECT file_id FROM fts_files WHERE fts_files MATCH ?) AS m
			JOIN indexed_files f ON f.id = m.file_id
			%s`, whereClause)
		queryArgs = append([]any{ftsExpr}, extraArgs...)
	} else {
		whereClause := "WHERE f.status = 'present'"
		if len(extraConds) > 0 {
			whereClause += " AND " + strings.Join(extraConds, " AND ")
		}
		queryStr = fmt.Sprintf(`SELECT COUNT(*) FROM indexed_files f %s`, whereClause)
		queryArgs = extraArgs
	}

	var n int
	err := s.db.QueryRowContext(ctx, queryStr, queryArgs...).Scan(&n)
	return n, err
}

// rankCursorCond builds a keyset condition and bind args for FTS pagination.
// Results are ordered by (rank, id), so a cursor encodes both halves to keep
// pages stable when the overall ranking is unchanged.
func rankCursorCond(cursor string) (string, []any) {
	rank, id, ok := decodeCursor(cursor)
	if !ok {
		return "", nil
	}
	return "(m.rank > ? OR (m.rank = ? AND f.id > ?))", []any{rank, rank, id}
}

// encodeCursor flattens a (rank, id) keyset value into an opaque string.
func encodeCursor(rank float64, id string) string {
	return strconv.FormatFloat(rank, 'g', -1, 64) + "|" + id
}

// decodeCursor reverses encodeCursor; it reports ok=false for anything malformed.
func decodeCursor(cursor string) (rank float64, id string, ok bool) {
	i := strings.LastIndexByte(cursor, '|')
	if i < 0 {
		return 0, "", false
	}
	r, err := strconv.ParseFloat(cursor[:i], 64)
	if err != nil {
		return 0, "", false
	}
	return r, cursor[i+1:], true
}

// --- row scanner ---

type rowScanner interface {
	Scan(dest ...any) error
}

// scanSearchFile scans one search row (including the trailing rank column,
// which the browse path pads with NULL) into a media.File.
func scanSearchFile(row rowScanner, libraryID string) (*media.File, float64, error) {
	var (
		f         media.File
		modStr    string
		firstStr  string
		lastStr   string
		hash      sql.NullString
		mediaType string
		status    string
		rank      sql.NullFloat64
	)
	err := row.Scan(
		&f.ID, &f.RelPath, &f.SizeBytes, &modStr, &hash, &mediaType, &status, &firstStr, &lastStr, &rank,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("scan search file: %w", err)
	}
	f.LibraryID = libraryID
	f.ContentHash = hash.String
	f.Status = media.FileStatus(status)
	f.Name = filepath.Base(f.RelPath)
	f.FolderPath = filepath.ToSlash(filepath.Dir(f.RelPath))
	if f.FolderPath == "." {
		f.FolderPath = ""
	}
	f.MediaType = media.MediaType(mediaType)
	f.MIMEType = media.DetectMIME(f.RelPath)
	if err := parseTime(modStr, &f.ModTime); err != nil {
		return nil, 0, err
	}
	if err := parseTime(firstStr, &f.FirstSeenAt); err != nil {
		return nil, 0, err
	}
	if err := parseTime(lastStr, &f.LastSeenAt); err != nil {
		return nil, 0, err
	}
	r := float64(0)
	if rank.Valid {
		r = rank.Float64
	}
	return &f, r, nil
}

func parseTime(s string, out *time.Time) error {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return fmt.Errorf("parse time %q: %w", s, err)
	}
	*out = t
	return nil
}
