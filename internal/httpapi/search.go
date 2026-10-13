package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/Jishnu-Prasad888/Orchard/internal/auth"
	"github.com/Jishnu-Prasad888/Orchard/internal/authz"
	"github.com/Jishnu-Prasad888/Orchard/internal/highlights"
	"github.com/Jishnu-Prasad888/Orchard/internal/library"
	"github.com/Jishnu-Prasad888/Orchard/internal/librarydb"
	"github.com/Jishnu-Prasad888/Orchard/internal/media"
	"github.com/Jishnu-Prasad888/Orchard/internal/quotes"
	"github.com/Jishnu-Prasad888/Orchard/internal/search"
)

// openSearchStore opens the per-library DB and returns a SearchStore.
func (s *Server) openSearchStore(
	w http.ResponseWriter, r *http.Request, lib *library.Library,
) (*search.SearchStore, func(), bool) {
	if lib.Status == library.StatusOffline {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusConflict,
			CodeConflict, "Library is offline.")
		return nil, nil, false
	}
	orchardDir := lib.Root + "/.orchard"
	db, err := librarydb.Open(orchardDir)
	if err != nil {
		s.logger.Error("open library db for search", "library_id", lib.ID, "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to open library database.")
		return nil, nil, false
	}
	store := search.NewSearchStore(db, lib.ID)
	return store, func() { _ = db.Close() }, true
}

// handleSearch — GET /api/v1/libraries/{id}/search
//
// Query parameters:
//
//	q       – FTS5 text expression
//	type    – media type filter (photo, video, audio, document, other)
//	folder  – folder path prefix filter
//	tag     – tag name filter (case-insensitive)
//	author  – book author filter (substring, case-insensitive)
//	collection – collection ID filter, scoped to the caller's own collections
//	min_size – minimum file size in bytes
//	max_size – maximum file size in bytes
//	from    – RFC3339 date lower bound on mod_time
//	to      – RFC3339 date upper bound on mod_time
//	cursor  – pagination cursor (rank|file_id for text search, file_id otherwise)
//	limit   – page size (default 50, max 200)
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	store, cleanup, ok := s.openSearchStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	q := parseSearchQuery(r)
	if q.CollectionID != "" {
		// A collection filter is always scoped to the caller's own
		// collections — never lets one user probe another's by ID.
		q.CollectionUserID = u.ID
	}
	folder, ok := canonicalFolder(q.FolderPath)
	if !ok {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusBadRequest,
			CodeBadRequest, "Invalid folder path.")
		return
	}
	q.FolderPath = folder
	if !s.requireCap(w, r, u, folderKeyFromParent(lib.ID, q.FolderPath), authz.CapRead) {
		return
	}
	page, err := store.Search(r.Context(), q)
	if err != nil {
		if errors.Is(err, search.ErrInvalidQuery) {
			writeError(w, s.logger, requestIDOrEmpty(r), http.StatusBadRequest,
				CodeBadRequest, "Invalid search query.")
			return
		}
		s.logger.Error("search files", "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to search files.")
		return
	}

	files := make([]*media.File, 0, len(page.Results))
	results := make([]fileResponse, 0, len(page.Results))
	for _, res := range page.Results {
		files = append(files, res.File)
		results = append(results, toFileResponse(res.File))
	}
	attachCoverURLs(results, s.coverURLsFor(r.Context(), lib, files))
	writeJSON(w, s.logger, http.StatusOK, map[string]any{
		"files":       results,
		"next_cursor": page.NextCursor,
		"total":       page.Total,
	})
}

// contentHitResponse is the API representation of a ContentHit.
type contentHitResponse struct {
	SourceType string `json:"source_type"`
	SourceID   string `json:"source_id"`
	FileID     string `json:"file_id,omitempty"`
	Excerpt    string `json:"excerpt"`
	// Location is the opaque reader position (see internal/bookmarks'
	// doc comment for the convention) to jump to, when known. Only quote
	// and highlight hits have one — fts_content itself doesn't carry a
	// location (it isn't searchable text), so this is filled in by a
	// best-effort follow-up lookup (see enrichContentLocations).
	Location string `json:"location,omitempty"`
}

func toContentHitResponses(hits []*search.ContentHit, locations map[string]string) []contentHitResponse {
	out := make([]contentHitResponse, 0, len(hits))
	for _, h := range hits {
		out = append(out, contentHitResponse{
			SourceType: string(h.SourceType),
			SourceID:   h.SourceID,
			FileID:     h.FileID,
			Excerpt:    h.Excerpt,
			Location:   locations[string(h.SourceType)+":"+h.SourceID],
		})
	}
	return out
}

// enrichContentLocations looks up the real location for quote/highlight
// hits, so "jump to matching location" has somewhere to go. Best-effort:
// on any failure it returns what it has rather than failing the whole
// search — this is enrichment of the primary result, not the result
// itself, so it never writes an HTTP response of its own.
func enrichContentLocations(ctx context.Context, lib *library.Library, userID string, hits []*search.ContentHit) map[string]string {
	locations := make(map[string]string)
	var needsQuotes, needsHighlights bool
	for _, h := range hits {
		switch h.SourceType {
		case search.ContentQuote:
			needsQuotes = true
		case search.ContentHighlight:
			needsHighlights = true
		}
	}
	if !needsQuotes && !needsHighlights {
		return locations
	}
	db, err := librarydb.Open(lib.Root + "/.orchard")
	if err != nil {
		return locations
	}
	defer func() { _ = db.Close() }()

	if needsQuotes {
		qs := quotes.NewStore(db)
		for _, h := range hits {
			if h.SourceType != search.ContentQuote {
				continue
			}
			if q, err := qs.GetOwned(ctx, h.SourceID, userID); err == nil {
				locations["quote:"+h.SourceID] = q.Location
			}
		}
	}
	if needsHighlights {
		hs := highlights.NewStore(db)
		for _, h := range hits {
			if h.SourceType != search.ContentHighlight {
				continue
			}
			if hl, err := hs.GetOwned(ctx, h.SourceID, userID); err == nil {
				locations["highlight:"+h.SourceID] = hl.Location
			}
		}
	}
	return locations
}

// handleSearchContent — GET /api/v1/libraries/{id}/search/content?q=...
//
// Searches extracted book text, book metadata, quotes, highlights, and
// notes content — everything in fts_content — as opposed to handleSearch,
// which only ever matches file paths.
func (s *Server) handleSearchContent(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapRead) {
		return
	}
	store, cleanup, ok := s.openSearchStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	hits, err := store.SearchContent(r.Context(), search.ContentQuery{
		Text:   r.URL.Query().Get("q"),
		UserID: u.ID,
	})
	if err != nil {
		if errors.Is(err, search.ErrInvalidQuery) {
			writeError(w, s.logger, requestIDOrEmpty(r), http.StatusBadRequest,
				CodeBadRequest, "Invalid search query.")
			return
		}
		s.logger.Error("search content", "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to search.")
		return
	}
	locations := enrichContentLocations(r.Context(), lib, u.ID, hits)
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"hits": toContentHitResponses(hits, locations)})
}

// handleSearchFileContent — GET /api/v1/libraries/{id}/files/{fileID}/search/content?q=...
//
// The reader-local variant of handleSearchContent: scoped to one book, so
// it can answer "does this book (or my notes/quotes/highlights about it)
// contain this term, and where" from the server-extracted text and
// annotations — complementing the reader's own client-side, open-book-only
// search (components/reader/*.tsx's ReaderHandle.search).
func (s *Server) handleSearchFileContent(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.fileKeyFor(w, r, u, lib, r.PathValue("fileID"), authz.CapRead) {
		return
	}
	store, cleanup, ok := s.openSearchStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	hits, err := store.SearchContent(r.Context(), search.ContentQuery{
		Text:   r.URL.Query().Get("q"),
		UserID: u.ID,
		FileID: r.PathValue("fileID"),
	})
	if err != nil {
		if errors.Is(err, search.ErrInvalidQuery) {
			writeError(w, s.logger, requestIDOrEmpty(r), http.StatusBadRequest,
				CodeBadRequest, "Invalid search query.")
			return
		}
		s.logger.Error("search file content", "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to search.")
		return
	}
	locations := enrichContentLocations(r.Context(), lib, u.ID, hits)
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"hits": toContentHitResponses(hits, locations)})
}

// parseSearchQuery extracts search parameters from the request query string.
func parseSearchQuery(r *http.Request) search.SearchQuery {
	q := r.URL.Query()
	sq := search.SearchQuery{
		Text:         q.Get("q"),
		Type:         media.MediaType(q.Get("type")),
		FolderPath:   q.Get("folder"),
		Tag:          q.Get("tag"),
		Author:       q.Get("author"),
		CollectionID: q.Get("collection"),
		Cursor:       q.Get("cursor"),
	}
	if v := q.Get("min_size"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			sq.MinSize = n
		}
	}
	if v := q.Get("max_size"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			sq.MaxSize = n
		}
	}
	if lim := q.Get("limit"); lim != "" {
		if n, err := strconv.Atoi(lim); err == nil {
			sq.Limit = n
		}
	}
	if from := q.Get("from"); from != "" {
		if t, err := time.Parse(time.RFC3339, from); err == nil {
			sq.DateFrom = t
		}
	}
	if to := q.Get("to"); to != "" {
		if t, err := time.Parse(time.RFC3339, to); err == nil {
			sq.DateTo = t
		}
	}
	return sq
}
