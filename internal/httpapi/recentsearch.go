package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/Jishnu-Prasad888/Orchard/internal/auth"
	"github.com/Jishnu-Prasad888/Orchard/internal/authz"
	"github.com/Jishnu-Prasad888/Orchard/internal/library"
	"github.com/Jishnu-Prasad888/Orchard/internal/librarydb"
	"github.com/Jishnu-Prasad888/Orchard/internal/recentsearch"
)

// openRecentSearchStore opens the per-library DB and returns a recentsearch.Store.
func (s *Server) openRecentSearchStore(
	w http.ResponseWriter, r *http.Request, lib *library.Library,
) (*recentsearch.Store, func(), bool) {
	if lib.Status == library.StatusOffline {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusConflict,
			CodeConflict, "Library is offline.")
		return nil, nil, false
	}
	db, release, err := librarydb.Acquire(lib.Root + "/.orchard")
	if err != nil {
		s.logger.Error("open library db for recent searches", "library_id", lib.ID, "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to open library database.")
		return nil, nil, false
	}
	return recentsearch.NewStore(db), release, true
}

type recentSearchResponse struct {
	Query      string `json:"query"`
	SearchedAt string `json:"searched_at"`
}

// handleListRecentSearches — GET /api/v1/libraries/{id}/search/recent?limit=N
func (s *Server) handleListRecentSearches(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapRead) {
		return
	}
	limit := recentsearch.MaxPerUser
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	store, cleanup, ok := s.openRecentSearchStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	entries, err := store.List(r.Context(), u.ID, limit)
	if err != nil {
		s.logger.Error("list recent searches", "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to list recent searches.")
		return
	}
	out := make([]recentSearchResponse, 0, len(entries))
	for _, e := range entries {
		out = append(out, recentSearchResponse{Query: e.Query, SearchedAt: e.SearchedAt.UTC().Format(time.RFC3339)})
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"searches": out})
}

// handleRecordRecentSearch — POST /api/v1/libraries/{id}/search/recent
// Fire-and-forget from the client's point of view: it never blocks or
// fails the actual search.
func (s *Server) handleRecordRecentSearch(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapRead) {
		return
	}
	var body struct {
		Query string `json:"query"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), err)
		return
	}
	store, cleanup, ok := s.openRecentSearchStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	if err := store.Record(r.Context(), u.ID, body.Query); err != nil {
		s.logger.Error("record recent search", "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to record the search.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleForgetRecentSearch — DELETE /api/v1/libraries/{id}/search/recent?q=...
func (s *Server) handleForgetRecentSearch(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapRead) {
		return
	}
	store, cleanup, ok := s.openRecentSearchStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	if err := store.Forget(r.Context(), u.ID, r.URL.Query().Get("q")); err != nil {
		s.logger.Error("forget recent search", "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to remove the search.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
