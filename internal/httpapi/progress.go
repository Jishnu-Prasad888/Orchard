package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/Jishnu-Prasad888/Orchard/internal/auth"
	"github.com/Jishnu-Prasad888/Orchard/internal/authz"
	"github.com/Jishnu-Prasad888/Orchard/internal/library"
	"github.com/Jishnu-Prasad888/Orchard/internal/librarydb"
	"github.com/Jishnu-Prasad888/Orchard/internal/media"
	"github.com/Jishnu-Prasad888/Orchard/internal/progress"
)

// defaultInProgressLimit matches Home's "a taste, not the library" sizing.
const defaultInProgressLimit = 12

// maxInProgressLimit bounds a caller-supplied ?limit=.
const maxInProgressLimit = 100

// openProgressStore opens the per-library DB and returns a progress.Store.
func (s *Server) openProgressStore(
	w http.ResponseWriter, r *http.Request, lib *library.Library,
) (*progress.Store, func(), bool) {
	if lib.Status == library.StatusOffline {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusConflict,
			CodeConflict, "Library is offline.")
		return nil, nil, false
	}
	db, release, err := librarydb.Acquire(lib.Root + "/.orchard")
	if err != nil {
		s.logger.Error("open library db for reading progress", "library_id", lib.ID, "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to open library database.")
		return nil, nil, false
	}
	return progress.NewStore(db), release, true
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

// inProgressFileResponse is a file plus the acting user's resume point in it.
type inProgressFileResponse struct {
	fileResponse
	Percent   float64 `json:"percent"`
	Location  string  `json:"location"`
	UpdatedAt string  `json:"updated_at,omitempty"`
}

// handleListInProgress — GET /api/v1/libraries/{id}/reading/in-progress?limit=N
// Returns the acting user's most recently updated unfinished books, for a
// "continue reading" surface.
func (s *Server) handleListInProgress(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapRead) {
		return
	}
	limit := defaultInProgressLimit
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = min(n, maxInProgressLimit)
		}
	}

	store, cleanup, ok := s.openProgressStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	entries, err := store.ListInProgress(r.Context(), u.ID, lib.ID, limit)
	if err != nil {
		s.logger.Error("list in-progress files", "library_id", lib.ID, "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to list in-progress files.")
		return
	}
	files := make([]*media.File, 0, len(entries))
	for _, e := range entries {
		files = append(files, e.File)
	}
	extras := s.fileExtrasFor(r.Context(), lib, u.ID, files)

	out := make([]inProgressFileResponse, 0, len(entries))
	for _, e := range entries {
		resp := inProgressFileResponse{
			fileResponse: toFileResponse(e.File),
			Percent:      e.Percent,
			Location:     e.Location,
		}
		if !e.UpdatedAt.IsZero() {
			resp.UpdatedAt = e.UpdatedAt.UTC().Format(time.RFC3339)
		}
		if url, ok := extras.covers[e.File.ID]; ok {
			resp.CoverURL = url
		}
		resp.Completed = extras.completed[e.File.ID]
		out = append(out, resp)
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"files": out})
}
