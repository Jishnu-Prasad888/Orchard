package httpapi

import (
	"net/http"
	"strconv"

	"github.com/Jishnu-Prasad888/Orchard/internal/auth"
	"github.com/Jishnu-Prasad888/Orchard/internal/authz"
	"github.com/Jishnu-Prasad888/Orchard/internal/library"
	"github.com/Jishnu-Prasad888/Orchard/internal/librarydb"
	"github.com/Jishnu-Prasad888/Orchard/internal/recents"
)

// defaultRecentlyOpenedLimit matches the grid's usual "a taste, not the
// library" sizing elsewhere (Home shows a couple of rows, not everything).
const defaultRecentlyOpenedLimit = 40

// maxRecentlyOpenedLimit bounds a caller-supplied ?limit= so a single
// request cannot ask for an unbounded scan.
const maxRecentlyOpenedLimit = 200

// openRecentsStore opens the per-library DB and returns a recents.Store.
func (s *Server) openRecentsStore(
	w http.ResponseWriter, r *http.Request, lib *library.Library,
) (*recents.Store, func(), bool) {
	if lib.Status == library.StatusOffline {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusConflict,
			CodeConflict, "Library is offline.")
		return nil, nil, false
	}
	db, release, err := librarydb.Acquire(lib.Root + "/.orchard")
	if err != nil {
		s.logger.Error("open library db for recents", "library_id", lib.ID, "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to open library database.")
		return nil, nil, false
	}
	return recents.NewStore(db, lib.ID), release, true
}

// handleMarkFileOpened — POST /api/v1/libraries/{id}/files/{fileID}/opened
// Records that the acting user opened this file just now, for the
// "recently opened" list. Fire-and-forget from the client's point of view:
// it never blocks or fails the actual file open.
func (s *Server) handleMarkFileOpened(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.fileKeyFor(w, r, u, lib, r.PathValue("fileID"), authz.CapRead) {
		return
	}
	store, cleanup, ok := s.openRecentsStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	if err := store.MarkOpened(r.Context(), u.ID, r.PathValue("fileID")); err != nil {
		s.logger.Error("mark file opened", "library_id", lib.ID, "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to record the open.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleListRecentlyOpened — GET /api/v1/libraries/{id}/files/recently-opened?limit=N
func (s *Server) handleListRecentlyOpened(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapRead) {
		return
	}
	limit := defaultRecentlyOpenedLimit
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = min(n, maxRecentlyOpenedLimit)
		}
	}

	store, cleanup, ok := s.openRecentsStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	files, err := store.ListRecentlyOpened(r.Context(), u.ID, limit)
	if err != nil {
		s.logger.Error("list recently opened", "library_id", lib.ID, "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to list recently opened files.")
		return
	}
	out := toFileResponses(files)
	attachFileExtras(out, s.fileExtrasFor(r.Context(), lib, u.ID, files))
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"files": out})
}
