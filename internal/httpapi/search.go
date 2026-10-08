package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/Jishnu-Prasad888/Cairn/internal/auth"
	"github.com/Jishnu-Prasad888/Cairn/internal/authz"
	"github.com/Jishnu-Prasad888/Cairn/internal/library"
	"github.com/Jishnu-Prasad888/Cairn/internal/librarydb"
	"github.com/Jishnu-Prasad888/Cairn/internal/media"
	"github.com/Jishnu-Prasad888/Cairn/internal/search"
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
	cairnDir := lib.Root + "/.cairn"
	db, err := librarydb.Open(cairnDir)
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
//	album   – album id filter
//	person  – person id filter (files containing a face assigned to person)
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

	results := make([]fileResponse, 0, len(page.Results))
	for _, res := range page.Results {
		results = append(results, toFileResponse(res.File))
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{
		"files":       results,
		"next_cursor": page.NextCursor,
		"total":       page.Total,
	})
}

// parseSearchQuery extracts search parameters from the request query string.
func parseSearchQuery(r *http.Request) search.SearchQuery {
	q := r.URL.Query()
	sq := search.SearchQuery{
		Text:       q.Get("q"),
		Type:       media.MediaType(q.Get("type")),
		FolderPath: q.Get("folder"),
		Tag:        q.Get("tag"),
		Cursor:     q.Get("cursor"),
	}
	if alb := q.Get("album"); alb != "" {
		sq.AlbumID = alb
	}
	if per := q.Get("person"); per != "" {
		sq.PersonID = per
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
