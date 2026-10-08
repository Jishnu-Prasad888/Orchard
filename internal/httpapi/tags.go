package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/Jishnu-Prasad888/Cairn/internal/auth"
	"github.com/Jishnu-Prasad888/Cairn/internal/authz"
	"github.com/Jishnu-Prasad888/Cairn/internal/library"
	"github.com/Jishnu-Prasad888/Cairn/internal/librarydb"
	"github.com/Jishnu-Prasad888/Cairn/internal/tags"
)

// openTagStore opens the per-library DB and returns a TagStore.
func (s *Server) openTagStore(
	w http.ResponseWriter, r *http.Request, lib *library.Library,
) (*tags.TagStore, func(), bool) {
	if lib.Status == library.StatusOffline {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusConflict,
			CodeConflict, "Library is offline.")
		return nil, nil, false
	}
	cairnDir := lib.Root + "/.cairn"
	db, err := librarydb.Open(cairnDir)
	if err != nil {
		s.logger.Error("open library db for tags", "library_id", lib.ID, "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to open library database.")
		return nil, nil, false
	}
	return tags.NewTagStore(db), func() { _ = db.Close() }, true
}

// tagResponse is the API representation of a tag.
type tagResponse struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Color     string `json:"color,omitempty"`
	CreatedAt string `json:"created_at"`
}

func toTagResponse(t *tags.Tag) tagResponse {
	return tagResponse{
		ID:        t.ID,
		Name:      t.Name,
		Color:     t.Color,
		CreatedAt: t.CreatedAt.UTC().Format(time.RFC3339),
	}
}

func toTagResponses(ts []*tags.Tag) []tagResponse {
	out := make([]tagResponse, 0, len(ts))
	for _, t := range ts {
		out = append(out, toTagResponse(t))
	}
	return out
}

// handleListTags — GET /api/v1/libraries/{id}/tags
func (s *Server) handleListTags(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapRead) {
		return
	}
	store, cleanup, ok := s.openTagStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	ts, err := store.List(r.Context())
	if err != nil {
		s.logger.Error("list tags", "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to list tags.")
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"tags": toTagResponses(ts)})
}

// handleCreateTag — POST /api/v1/libraries/{id}/tags
func (s *Server) handleCreateTag(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapCreate) {
		return
	}
	store, cleanup, ok := s.openTagStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	var body struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), err)
		return
	}
	if body.Name == "" {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusBadRequest,
			CodeBadRequest, "name is required.")
		return
	}
	t, err := store.Create(r.Context(), body.Name, body.Color)
	if err != nil {
		s.writeTagError(w, r, err)
		return
	}
	writeJSON(w, s.logger, http.StatusCreated, map[string]any{"tag": toTagResponse(t)})
}

// handleDeleteTag — DELETE /api/v1/libraries/{id}/tags/{tagID}
func (s *Server) handleDeleteTag(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.requireCap(w, r, u, authz.EntityKey("t", lib.ID, r.PathValue("tagID")), authz.CapDelete) {
		return
	}
	store, cleanup, ok := s.openTagStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	if err := store.Delete(r.Context(), r.PathValue("tagID")); err != nil {
		s.writeTagError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleAddFileTag — POST /api/v1/libraries/{id}/files/{fileID}/tags
func (s *Server) handleAddFileTag(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.fileKeyFor(w, r, u, lib, r.PathValue("fileID"), authz.CapEdit) {
		return
	}
	store, cleanup, ok := s.openTagStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	var body struct {
		TagID string `json:"tag_id"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), err)
		return
	}
	if body.TagID == "" {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusBadRequest,
			CodeBadRequest, "tag_id is required.")
		return
	}
	if err := store.Attach(r.Context(), r.PathValue("fileID"), body.TagID); err != nil {
		s.writeTagError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRemoveFileTag — DELETE /api/v1/libraries/{id}/files/{fileID}/tags/{tagID}
func (s *Server) handleRemoveFileTag(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.fileKeyFor(w, r, u, lib, r.PathValue("fileID"), authz.CapEdit) {
		return
	}
	store, cleanup, ok := s.openTagStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	if err := store.Detach(r.Context(), r.PathValue("fileID"), r.PathValue("tagID")); err != nil {
		s.writeTagError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleListFileTags — GET /api/v1/libraries/{id}/files/{fileID}/tags
func (s *Server) handleListFileTags(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.fileKeyFor(w, r, u, lib, r.PathValue("fileID"), authz.CapRead) {
		return
	}
	store, cleanup, ok := s.openTagStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	ts, err := store.ListByFile(r.Context(), r.PathValue("fileID"))
	if err != nil {
		s.logger.Error("list file tags", "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to list tags.")
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"tags": toTagResponses(ts)})
}

// writeTagError maps tag domain errors to HTTP responses.
func (s *Server) writeTagError(w http.ResponseWriter, r *http.Request, err error) {
	reqID := requestIDOrEmpty(r)
	switch {
	case errors.Is(err, tags.ErrNotFound):
		writeError(w, s.logger, reqID, http.StatusNotFound, CodeNotFound, "Tag not found.")
	case errors.Is(err, tags.ErrAlreadyExists):
		writeError(w, s.logger, reqID, http.StatusConflict, CodeConflict,
			"A tag with that name already exists.")
	case errors.Is(err, tags.ErrFileNotTagged):
		writeError(w, s.logger, reqID, http.StatusNotFound, CodeNotFound,
			"Tag is not attached to this file.")
	default:
		s.logger.Error("tag operation", "error", err)
		writeError(w, s.logger, reqID, http.StatusInternalServerError,
			CodeInternal, "Internal server error.")
	}
}
