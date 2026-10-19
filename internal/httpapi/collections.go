package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/Jishnu-Prasad888/Orchard/internal/auth"
	"github.com/Jishnu-Prasad888/Orchard/internal/authz"
	"github.com/Jishnu-Prasad888/Orchard/internal/collections"
	"github.com/Jishnu-Prasad888/Orchard/internal/library"
	"github.com/Jishnu-Prasad888/Orchard/internal/librarydb"
)

// openCollectionStore opens the per-library DB and returns a collections.Store.
func (s *Server) openCollectionStore(
	w http.ResponseWriter, r *http.Request, lib *library.Library,
) (*collections.Store, func(), bool) {
	if lib.Status == library.StatusOffline {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusConflict,
			CodeConflict, "Library is offline.")
		return nil, nil, false
	}
	orchardDir := lib.Root + "/.orchard"
	db, release, err := librarydb.Acquire(orchardDir)
	if err != nil {
		s.logger.Error("open library db for collections", "library_id", lib.ID, "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to open library database.")
		return nil, nil, false
	}
	return collections.NewStore(db, lib.ID), release, true
}

// collectionResponse is the API representation of a collection.
type collectionResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Pinned      bool   `json:"pinned"`
	ItemCount   int    `json:"item_count"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

func toCollectionResponse(c *collections.Collection) collectionResponse {
	return collectionResponse{
		ID:          c.ID,
		Name:        c.Name,
		Description: c.Description,
		Pinned:      c.Pinned,
		ItemCount:   c.ItemCount,
		CreatedAt:   c.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:   c.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func toCollectionResponses(cs []*collections.Collection) []collectionResponse {
	out := make([]collectionResponse, 0, len(cs))
	for _, c := range cs {
		out = append(out, toCollectionResponse(c))
	}
	return out
}

// handleListCollections — GET /api/v1/libraries/{id}/collections
func (s *Server) handleListCollections(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapRead) {
		return
	}
	store, cleanup, ok := s.openCollectionStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	cs, err := store.List(r.Context(), u.ID)
	if err != nil {
		s.logger.Error("list collections", "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to list collections.")
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"collections": toCollectionResponses(cs)})
}

// handleCreateCollection — POST /api/v1/libraries/{id}/collections
func (s *Server) handleCreateCollection(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapCreate) {
		return
	}
	store, cleanup, ok := s.openCollectionStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
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
	c, err := store.Create(r.Context(), u.ID, body.Name, body.Description)
	if err != nil {
		s.writeCollectionError(w, r, err)
		return
	}
	writeJSON(w, s.logger, http.StatusCreated, map[string]any{"collection": toCollectionResponse(c)})
}

// handleUpdateCollection — PATCH /api/v1/libraries/{id}/collections/{collectionID}
func (s *Server) handleUpdateCollection(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapEdit) {
		return
	}
	store, cleanup, ok := s.openCollectionStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	var body struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
		Pinned      *bool   `json:"pinned"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), err)
		return
	}
	if body.Name != nil && *body.Name == "" {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusBadRequest,
			CodeBadRequest, "name must not be empty.")
		return
	}
	c, err := store.Update(r.Context(), r.PathValue("collectionID"), u.ID, collections.Update{
		Name:        body.Name,
		Description: body.Description,
		Pinned:      body.Pinned,
	})
	if err != nil {
		s.writeCollectionError(w, r, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"collection": toCollectionResponse(c)})
}

// handleDeleteCollection — DELETE /api/v1/libraries/{id}/collections/{collectionID}
func (s *Server) handleDeleteCollection(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapDelete) {
		return
	}
	store, cleanup, ok := s.openCollectionStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	if err := store.Delete(r.Context(), r.PathValue("collectionID"), u.ID); err != nil {
		s.writeCollectionError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleListCollectionFiles — GET /api/v1/libraries/{id}/collections/{collectionID}/files
func (s *Server) handleListCollectionFiles(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapRead) {
		return
	}
	store, cleanup, ok := s.openCollectionStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	files, err := store.ListFiles(r.Context(), r.PathValue("collectionID"), u.ID)
	if err != nil {
		s.writeCollectionError(w, r, err)
		return
	}
	results := make([]fileResponse, 0, len(files))
	for _, f := range files {
		results = append(results, toFileResponse(f))
	}
	attachFileExtras(results, s.fileExtrasFor(r.Context(), lib, u.ID, files))
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"files": results})
}

// handleAddCollectionFile — POST /api/v1/libraries/{id}/collections/{collectionID}/files
func (s *Server) handleAddCollectionFile(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapEdit) {
		return
	}
	store, cleanup, ok := s.openCollectionStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	var body struct {
		FileID string `json:"file_id"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), err)
		return
	}
	if body.FileID == "" {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusBadRequest,
			CodeBadRequest, "file_id is required.")
		return
	}
	if err := store.AddFile(r.Context(), r.PathValue("collectionID"), u.ID, body.FileID); err != nil {
		s.writeCollectionError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRemoveCollectionFile — DELETE /api/v1/libraries/{id}/collections/{collectionID}/files/{fileID}
func (s *Server) handleRemoveCollectionFile(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapEdit) {
		return
	}
	store, cleanup, ok := s.openCollectionStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	err = store.RemoveFile(r.Context(), r.PathValue("collectionID"), u.ID, r.PathValue("fileID"))
	if err != nil {
		s.writeCollectionError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleListFileCollections — GET /api/v1/libraries/{id}/files/{fileID}/collections
//
// Returns the caller's own collections that contain this file — used by the
// viewer's "add to collection" picker to show which are already checked.
func (s *Server) handleListFileCollections(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.fileKeyFor(w, r, u, lib, r.PathValue("fileID"), authz.CapRead) {
		return
	}
	store, cleanup, ok := s.openCollectionStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	cs, err := store.ListByFile(r.Context(), u.ID, r.PathValue("fileID"))
	if err != nil {
		s.logger.Error("list file collections", "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to list collections.")
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"collections": toCollectionResponses(cs)})
}

// writeCollectionError maps collection domain errors to HTTP responses.
func (s *Server) writeCollectionError(w http.ResponseWriter, r *http.Request, err error) {
	reqID := requestIDOrEmpty(r)
	switch {
	case errors.Is(err, collections.ErrNotFound):
		writeError(w, s.logger, reqID, http.StatusNotFound, CodeNotFound, "Collection not found.")
	case errors.Is(err, collections.ErrAlreadyExists):
		writeError(w, s.logger, reqID, http.StatusConflict, CodeConflict,
			"A collection with that name already exists.")
	case errors.Is(err, collections.ErrFileNotInCollection):
		writeError(w, s.logger, reqID, http.StatusNotFound, CodeNotFound,
			"File is not in this collection.")
	default:
		s.logger.Error("collection operation", "error", err)
		writeError(w, s.logger, reqID, http.StatusInternalServerError,
			CodeInternal, "Internal server error.")
	}
}
