package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/Jishnu-Prasad888/Orchard/internal/auth"
	"github.com/Jishnu-Prasad888/Orchard/internal/authz"
	"github.com/Jishnu-Prasad888/Orchard/internal/highlights"
	"github.com/Jishnu-Prasad888/Orchard/internal/library"
	"github.com/Jishnu-Prasad888/Orchard/internal/librarydb"
)

// openHighlightStore opens the per-library DB and returns a highlights.Store.
func (s *Server) openHighlightStore(
	w http.ResponseWriter, r *http.Request, lib *library.Library,
) (*highlights.Store, func(), bool) {
	if lib.Status == library.StatusOffline {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusConflict,
			CodeConflict, "Library is offline.")
		return nil, nil, false
	}
	db, release, err := librarydb.Acquire(lib.Root + "/.orchard")
	if err != nil {
		s.logger.Error("open library db for highlights", "library_id", lib.ID, "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to open library database.")
		return nil, nil, false
	}
	return highlights.NewStore(db), release, true
}

// highlightResponse is the API representation of a highlight.
type highlightResponse struct {
	ID        string `json:"id"`
	FileID    string `json:"file_id"`
	Location  string `json:"location"`
	Text      string `json:"text"`
	Color     string `json:"color"`
	Note      string `json:"note,omitempty"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

func toHighlightResponse(h *highlights.Highlight) highlightResponse {
	return highlightResponse{
		ID:        h.ID,
		FileID:    h.FileID,
		Location:  h.Location,
		Text:      h.Text,
		Color:     h.Color,
		Note:      h.Note,
		CreatedAt: h.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt: h.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func toHighlightResponses(hs []*highlights.Highlight) []highlightResponse {
	out := make([]highlightResponse, 0, len(hs))
	for _, h := range hs {
		out = append(out, toHighlightResponse(h))
	}
	return out
}

// handleListLibraryHighlights — GET /api/v1/libraries/{id}/highlights
func (s *Server) handleListLibraryHighlights(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapRead) {
		return
	}
	store, cleanup, ok := s.openHighlightStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	hs, err := store.List(r.Context(), u.ID)
	if err != nil {
		s.logger.Error("list highlights", "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to list highlights.")
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"highlights": toHighlightResponses(hs)})
}

// handleListFileHighlights — GET /api/v1/libraries/{id}/files/{fileID}/highlights
func (s *Server) handleListFileHighlights(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.fileKeyFor(w, r, u, lib, r.PathValue("fileID"), authz.CapRead) {
		return
	}
	store, cleanup, ok := s.openHighlightStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	hs, err := store.ListByFile(r.Context(), u.ID, r.PathValue("fileID"))
	if err != nil {
		s.logger.Error("list file highlights", "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to list highlights.")
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"highlights": toHighlightResponses(hs)})
}

// handleCreateHighlight — POST /api/v1/libraries/{id}/files/{fileID}/highlights
func (s *Server) handleCreateHighlight(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.fileKeyFor(w, r, u, lib, r.PathValue("fileID"), authz.CapEdit) {
		return
	}
	var body struct {
		Location string `json:"location"`
		Text     string `json:"text"`
		Color    string `json:"color"`
		Note     string `json:"note"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), err)
		return
	}
	if body.Location == "" {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusBadRequest,
			CodeBadRequest, "location is required.")
		return
	}
	store, cleanup, ok := s.openHighlightStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	h, err := store.Create(r.Context(), u.ID, r.PathValue("fileID"), body.Location, body.Text, body.Color, body.Note)
	if err != nil {
		s.writeHighlightError(w, r, err)
		return
	}
	writeJSON(w, s.logger, http.StatusCreated, map[string]any{"highlight": toHighlightResponse(h)})
}

// handleUpdateHighlight — PATCH /api/v1/libraries/{id}/highlights/{highlightID}
func (s *Server) handleUpdateHighlight(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapEdit) {
		return
	}
	var body struct {
		Color *string `json:"color"`
		Note  *string `json:"note"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), err)
		return
	}
	store, cleanup, ok := s.openHighlightStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	h, err := store.Update(r.Context(), r.PathValue("highlightID"), u.ID, highlights.Update{
		Color: body.Color,
		Note:  body.Note,
	})
	if err != nil {
		s.writeHighlightError(w, r, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"highlight": toHighlightResponse(h)})
}

// handleDeleteHighlight — DELETE /api/v1/libraries/{id}/highlights/{highlightID}
func (s *Server) handleDeleteHighlight(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapDelete) {
		return
	}
	store, cleanup, ok := s.openHighlightStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	if err := store.Delete(r.Context(), r.PathValue("highlightID"), u.ID); err != nil {
		s.writeHighlightError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeHighlightError maps highlight domain errors to HTTP responses.
func (s *Server) writeHighlightError(w http.ResponseWriter, r *http.Request, err error) {
	reqID := requestIDOrEmpty(r)
	switch {
	case errors.Is(err, highlights.ErrNotFound):
		writeError(w, s.logger, reqID, http.StatusNotFound, CodeNotFound, "Highlight not found.")
	default:
		s.logger.Error("highlight operation", "error", err)
		writeError(w, s.logger, reqID, http.StatusInternalServerError,
			CodeInternal, "Internal server error.")
	}
}
