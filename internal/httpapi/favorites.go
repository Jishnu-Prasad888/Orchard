package httpapi

import (
	"errors"
	"net/http"

	"github.com/Jishnu-Prasad888/Cairn/internal/auth"
	"github.com/Jishnu-Prasad888/Cairn/internal/authz"
	"github.com/Jishnu-Prasad888/Cairn/internal/favorites"
	"github.com/Jishnu-Prasad888/Cairn/internal/library"
	"github.com/Jishnu-Prasad888/Cairn/internal/librarydb"
)

// openFavoriteStore opens the per-library DB and returns a FavoriteStore.
func (s *Server) openFavoriteStore(
	w http.ResponseWriter, r *http.Request, lib *library.Library,
) (*favorites.FavoriteStore, func(), bool) {
	if lib.Status == library.StatusOffline {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusConflict,
			CodeConflict, "Library is offline.")
		return nil, nil, false
	}
	cairnDir := lib.Root + "/.cairn"
	db, err := librarydb.Open(cairnDir)
	if err != nil {
		s.logger.Error("open library db for favorites", "library_id", lib.ID, "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to open library database.")
		return nil, nil, false
	}
	return favorites.NewFavoriteStore(db, lib.ID), func() { _ = db.Close() }, true
}

// handleAddFavorite — POST /api/v1/libraries/{id}/files/{fileID}/favorite
func (s *Server) handleAddFavorite(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.fileKeyFor(w, r, u, lib, r.PathValue("fileID"), authz.CapEdit) {
		return
	}
	store, cleanup, ok := s.openFavoriteStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	if err := store.Add(r.Context(), r.PathValue("fileID")); err != nil {
		s.writeFavoriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRemoveFavorite — DELETE /api/v1/libraries/{id}/files/{fileID}/favorite
func (s *Server) handleRemoveFavorite(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.fileKeyFor(w, r, u, lib, r.PathValue("fileID"), authz.CapEdit) {
		return
	}
	store, cleanup, ok := s.openFavoriteStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	if err := store.Remove(r.Context(), r.PathValue("fileID")); err != nil {
		s.writeFavoriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleListFavorites — GET /api/v1/libraries/{id}/favorites
func (s *Server) handleListFavorites(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapRead) {
		return
	}
	store, cleanup, ok := s.openFavoriteStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	files, err := store.ListFiles(r.Context())
	if err != nil {
		s.logger.Error("list favorites", "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to list favorites.")
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"files": toFileResponses(files)})
}

// writeFavoriteError maps favorites domain errors to HTTP responses.
func (s *Server) writeFavoriteError(w http.ResponseWriter, r *http.Request, err error) {
	reqID := requestIDOrEmpty(r)
	switch {
	case errors.Is(err, favorites.ErrAlreadyFavorited):
		writeError(w, s.logger, reqID, http.StatusConflict, CodeConflict,
			"File is already in favorites.")
	case errors.Is(err, favorites.ErrNotFavorited):
		writeError(w, s.logger, reqID, http.StatusNotFound, CodeNotFound,
			"File is not in favorites.")
	default:
		s.logger.Error("favorites operation", "error", err)
		writeError(w, s.logger, reqID, http.StatusInternalServerError,
			CodeInternal, "Internal server error.")
	}
}
