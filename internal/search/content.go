package search

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/Jishnu-Prasad888/Orchard/internal/fts"
)

// ContentSourceType identifies what kind of row a ContentHit came from.
type ContentSourceType string

const (
	ContentBookText  ContentSourceType = "book_text"
	ContentMetadata  ContentSourceType = "metadata"
	ContentQuote     ContentSourceType = "quote"
	ContentHighlight ContentSourceType = "highlight"
	ContentNote      ContentSourceType = "note"
)

// ContentQuery searches the content of extracted book text, book metadata,
// quotes, highlights, and notes — everything indexed into fts_content, as
// opposed to Search/SearchQuery, which only ever matches file paths.
type ContentQuery struct {
	// Text is the search expression. See fts.BuildExpression for the
	// accepted syntax.
	Text string
	// UserID scopes per-user content (quotes/highlights/notes) to this
	// user; library-wide content (book text/metadata) is always included
	// regardless. Required.
	UserID string
	// FileID, when set, restricts results to content about this one file —
	// the reader-local "search this book" variant.
	FileID string
	// Limit caps the number of hits returned. Defaults to 30, capped at 100.
	Limit int
}

func (q *ContentQuery) defaults() {
	if q.Limit <= 0 {
		q.Limit = 30
	}
	if q.Limit > 100 {
		q.Limit = 100
	}
}

// ContentHit is a single match in fts_content.
type ContentHit struct {
	SourceType ContentSourceType
	SourceID   string
	FileID     string
	// Excerpt is a short window of the matched text around the query, for
	// a results list — the same "N chars before/after, ellipsis" convention
	// the in-reader client-side search already uses (see
	// components/reader/*.tsx's SearchMatch.excerpt), so both results lists
	// read the same way.
	Excerpt string
}

// SearchContent runs a full-text search over fts_content.
func (s *SearchStore) SearchContent(ctx context.Context, q ContentQuery) ([]*ContentHit, error) {
	q.defaults()
	if strings.TrimSpace(q.Text) == "" {
		return nil, nil
	}
	ftsExpr, err := fts.BuildExpression(q.Text)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidQuery, err)
	}
	if ftsExpr == "" {
		return nil, nil
	}

	conds := []string{"fts_content MATCH ?"}
	args := []any{ftsExpr}
	conds = append(conds, "(user_id = '' OR user_id = ?)")
	args = append(args, q.UserID)
	if q.FileID != "" {
		conds = append(conds, "file_id = ?")
		args = append(args, q.FileID)
	}
	queryStr := fmt.Sprintf(`
		SELECT source_type, source_id, file_id, text
		FROM fts_content
		WHERE %s
		ORDER BY rank
		LIMIT ?`, strings.Join(conds, " AND "))
	args = append(args, q.Limit)

	rows, err := s.db.QueryContext(ctx, queryStr, args...)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidQuery, err)
	}
	defer func() { _ = rows.Close() }()

	var hits []*ContentHit
	for rows.Next() {
		var sourceType, sourceID, fileID, text string
		if err := rows.Scan(&sourceType, &sourceID, &fileID, &text); err != nil {
			return nil, err
		}
		hits = append(hits, &ContentHit{
			SourceType: ContentSourceType(sourceType),
			SourceID:   sourceID,
			FileID:     fileID,
			Excerpt:    excerptAround(text, q.Text, 60),
		})
	}
	return hits, rows.Err()
}

// excerptAround returns a window of text centered on the first occurrence
// of needle (case-insensitive), falling back to needle's first word, and
// finally to the start of text if neither is found — a query with FTS5
// operators (AND/OR/"phrase") won't appear verbatim in the matched text, so
// this degrades gracefully rather than returning nothing.
func excerptAround(text, needle string, radius int) string {
	lowerText := strings.ToLower(text)
	lowerNeedle := strings.ToLower(strings.TrimSpace(needle))
	at := -1
	matchLen := 0
	if lowerNeedle != "" {
		if i := strings.Index(lowerText, lowerNeedle); i != -1 {
			at, matchLen = i, len(lowerNeedle)
		} else if fields := strings.Fields(lowerNeedle); len(fields) > 0 {
			if i := strings.Index(lowerText, fields[0]); i != -1 {
				at, matchLen = i, len(fields[0])
			}
		}
	}
	if at == -1 {
		if len(text) <= radius*2 {
			return strings.TrimSpace(text)
		}
		return strings.TrimSpace(text[:radius*2]) + "…"
	}
	start := max(0, at-radius)
	end := min(len(text), at+matchLen+radius)
	excerpt := strings.TrimSpace(text[start:end])
	if start > 0 {
		excerpt = "…" + excerpt
	}
	if end < len(text) {
		excerpt += "…"
	}
	return excerpt
}

// SyncContent replaces fts_content's row(s) for one (sourceType, sourceID),
// used by internal/bookmeta for book_metadata/book_text — content that is
// either written via UPSERT (whose trigger-firing semantics aren't worth
// depending on) or has no DB row of its own to trigger off (book text,
// which lives in a file). Quotes/highlights/notes are kept in sync by
// triggers instead (see the schema comment on fts_content in
// internal/librarydb) since neither of those risks applies to them. An
// empty text removes the row without replacing it.
func SyncContent(ctx context.Context, db *sql.DB, sourceType ContentSourceType, sourceID, fileID, userID, text string) error {
	if _, err := db.ExecContext(ctx,
		`DELETE FROM fts_content WHERE source_type = ? AND source_id = ?`, string(sourceType), sourceID); err != nil {
		return fmt.Errorf("sync fts_content: %w", err)
	}
	if strings.TrimSpace(text) == "" {
		return nil
	}
	_, err := db.ExecContext(ctx,
		`INSERT INTO fts_content (source_type, source_id, file_id, user_id, text) VALUES (?, ?, ?, ?, ?)`,
		string(sourceType), sourceID, fileID, userID, text)
	if err != nil {
		return fmt.Errorf("sync fts_content: %w", err)
	}
	return nil
}
