package httpapi

import (
	"net/http"
	"time"

	"github.com/Jishnu-Prasad888/Orchard/internal/auth"
	"github.com/Jishnu-Prasad888/Orchard/internal/authz"
	"github.com/Jishnu-Prasad888/Orchard/internal/library"
	"github.com/Jishnu-Prasad888/Orchard/internal/librarydb"
	"github.com/Jishnu-Prasad888/Orchard/internal/progress"
)

// openProgressStore opens the per-library DB and returns a progress.Store.
func (s *Server) openProgressStore(
	w http.ResponseWriter, r *http.Request, lib *library.Library,
) (*progress.Store, func(), bool) {
	if lib.Status == library.StatusOffline {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusConflict,
			CodeConflict, "Library is offline.")
		return nil, nil, false
	}
	db, err := librarydb.Open(lib.Root + "/.orchard")
	if err != nil {
		s.logger.Error("open library db for reading progress", "library_id", lib.ID, "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to open library database.")
		return nil, nil, false
	}
	return progress.NewStore(db), func() { _ = db.Close() }, true
}

// progressResponse is the wire representation of a user's resume point.
type progressResponse struct {
	FileID    string  `json:"file_id"`
	Location  string  `json:"location"`
	Percent   float64 `json:"percent"`
	UpdatedAt string  `json:"updated_at,omitempty"`
}

func toProgressResponse(p progress.Progress) progressResponse {
	resp := progressResponse{FileID: p.FileID, Location: p.Location, Percent: p.Percent}
	if !p.UpdatedAt.IsZero() {
		resp.UpdatedAt = p.UpdatedAt.UTC().Format(time.RFC3339)
	}
	return resp
}

// handleGetReadingProgress — GET /api/v1/libraries/{id}/files/{fileID}/progress
// Returns {"progress": null} when the acting user has no recorded position.
func (s *Server) handleGetReadingProgress(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.fileKeyFor(w, r, u, lib, r.PathValue("fileID"), authz.CapRead) {
		return
	}
	store, cleanup, ok := s.openProgressStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	p, has, err := store.Get(r.Context(), u.ID, r.PathValue("fileID"))
	if err != nil {
		s.logger.Error("get reading progress", "library_id", lib.ID, "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to read reading progress.")
		return
	}
	resp := map[string]any{"progress": nil}
	if has {
		p.FileID = r.PathValue("fileID")
		resp["progress"] = toProgressResponse(p)
	}
	writeJSON(w, s.logger, http.StatusOK, resp)
}

// handleSetReadingProgress — PUT /api/v1/libraries/{id}/files/{fileID}/progress
func (s *Server) handleSetReadingProgress(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.fileKeyFor(w, r, u, lib, r.PathValue("fileID"), authz.CapEdit) {
		return
	}
	var body struct {
		Location string  `json:"location"`
		Percent  float64 `json:"percent"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), err)
		return
	}
	if body.Percent < 0 || body.Percent > 100 {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusBadRequest, CodeBadRequest,
			"percent must be between 0 and 100.")
		return
	}

	store, cleanup, ok := s.openProgressStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	fileID := r.PathValue("fileID")
	if err := store.Set(r.Context(), u.ID, fileID, body.Location, body.Percent); err != nil {
		s.logger.Error("set reading progress", "library_id", lib.ID, "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to save reading progress.")
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{
		"progress": progressResponse{FileID: fileID, Location: body.Location, Percent: body.Percent},
	})
}

// handleClearReadingProgress — DELETE /api/v1/libraries/{id}/files/{fileID}/progress
func (s *Server) handleClearReadingProgress(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.fileKeyFor(w, r, u, lib, r.PathValue("fileID"), authz.CapEdit) {
		return
	}
	store, cleanup, ok := s.openProgressStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	if err := store.Clear(r.Context(), u.ID, r.PathValue("fileID")); err != nil {
		s.logger.Error("clear reading progress", "library_id", lib.ID, "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to clear reading progress.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
