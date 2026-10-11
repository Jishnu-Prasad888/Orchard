package httpapi

import (
	"net/http"

	"github.com/Jishnu-Prasad888/Orchard/internal/auth"
	"github.com/Jishnu-Prasad888/Orchard/internal/authz"
	"github.com/Jishnu-Prasad888/Orchard/internal/library"
	"github.com/Jishnu-Prasad888/Orchard/internal/librarydb"
	"github.com/Jishnu-Prasad888/Orchard/internal/reading"
)

// openReadingStore opens the per-library DB and returns a reading.Store.
func (s *Server) openReadingStore(
	w http.ResponseWriter, r *http.Request, lib *library.Library,
) (*reading.Store, func(), bool) {
	if lib.Status == library.StatusOffline {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusConflict,
			CodeConflict, "Library is offline.")
		return nil, nil, false
	}
	db, err := librarydb.Open(lib.Root + "/.orchard")
	if err != nil {
		s.logger.Error("open library db for reading status", "library_id", lib.ID, "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to open library database.")
		return nil, nil, false
	}
	return reading.NewStore(db), func() { _ = db.Close() }, true
}

// handleGetReadingStatus — GET /api/v1/libraries/{id}/files/{fileID}/reading-status
// Returns {"status": null} when the acting user has not set one.
func (s *Server) handleGetReadingStatus(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.fileKeyFor(w, r, u, lib, r.PathValue("fileID"), authz.CapRead) {
		return
	}
	store, cleanup, ok := s.openReadingStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	status, has, err := store.Get(r.Context(), u.ID, r.PathValue("fileID"))
	if err != nil {
		s.logger.Error("get reading status", "library_id", lib.ID, "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to read reading status.")
		return
	}
	resp := map[string]any{"status": nil}
	if has {
		resp["status"] = string(status)
	}
	writeJSON(w, s.logger, http.StatusOK, resp)
}

// handleSetReadingStatus — PUT /api/v1/libraries/{id}/files/{fileID}/reading-status
func (s *Server) handleSetReadingStatus(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.fileKeyFor(w, r, u, lib, r.PathValue("fileID"), authz.CapEdit) {
		return
	}
	var body struct {
		Status string `json:"status"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), err)
		return
	}
	status := reading.Status(body.Status)
	if !status.Valid() {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusBadRequest, CodeBadRequest,
			`status must be one of "want_to_read", "reading", "completed", "on_hold".`)
		return
	}

	store, cleanup, ok := s.openReadingStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	if err := store.Set(r.Context(), u.ID, r.PathValue("fileID"), status); err != nil {
		s.logger.Error("set reading status", "library_id", lib.ID, "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to save reading status.")
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"status": string(status)})
}

// handleClearReadingStatus — DELETE /api/v1/libraries/{id}/files/{fileID}/reading-status
func (s *Server) handleClearReadingStatus(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.fileKeyFor(w, r, u, lib, r.PathValue("fileID"), authz.CapEdit) {
		return
	}
	store, cleanup, ok := s.openReadingStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	if err := store.Clear(r.Context(), u.ID, r.PathValue("fileID")); err != nil {
		s.logger.Error("clear reading status", "library_id", lib.ID, "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to clear reading status.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
