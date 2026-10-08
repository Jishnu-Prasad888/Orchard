package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/Jishnu-Prasad888/Cairn/internal/albums"
	"github.com/Jishnu-Prasad888/Cairn/internal/auth"
	"github.com/Jishnu-Prasad888/Cairn/internal/authz"
	"github.com/Jishnu-Prasad888/Cairn/internal/library"
	"github.com/Jishnu-Prasad888/Cairn/internal/librarydb"
)

// openAlbumStore opens the per-library DB and returns an AlbumStore.
func (s *Server) openAlbumStore(
	w http.ResponseWriter, r *http.Request, lib *library.Library,
) (*albums.AlbumStore, func(), bool) {
	if lib.Status == library.StatusOffline {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusConflict,
			CodeConflict, "Library is offline.")
		return nil, nil, false
	}
	cairnDir := lib.Root + "/.cairn"
	db, err := librarydb.Open(cairnDir)
	if err != nil {
		s.logger.Error("open library db for albums", "library_id", lib.ID, "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to open library database.")
		return nil, nil, false
	}
	return albums.NewAlbumStore(db, lib.ID), func() { _ = db.Close() }, true
}

// albumResponse is the API representation of an album.
type albumResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	CoverFileID string `json:"cover_file_id,omitempty"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
	// FileCount and PreviewFileID let a client draw an album grid (cover and
	// "24 items") from the list alone, without a request per album.
	FileCount     int    `json:"file_count"`
	PreviewFileID string `json:"preview_file_id,omitempty"`
}

func toAlbumResponse(a *albums.Album) albumResponse {
	return albumResponse{
		ID:            a.ID,
		Name:          a.Name,
		Description:   a.Description,
		CoverFileID:   a.CoverFileID,
		CreatedAt:     a.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:     a.UpdatedAt.UTC().Format(time.RFC3339),
		FileCount:     a.FileCount,
		PreviewFileID: a.PreviewFileID,
	}
}

func toAlbumResponses(as []*albums.Album) []albumResponse {
	out := make([]albumResponse, 0, len(as))
	for _, a := range as {
		out = append(out, toAlbumResponse(a))
	}
	return out
}

// handleListAlbums — GET /api/v1/libraries/{id}/albums
func (s *Server) handleListAlbums(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapRead) {
		return
	}
	store, cleanup, ok := s.openAlbumStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	as, err := store.List(r.Context())
	if err != nil {
		s.logger.Error("list albums", "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to list albums.")
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"albums": toAlbumResponses(as)})
}

// handleCreateAlbum — POST /api/v1/libraries/{id}/albums
func (s *Server) handleCreateAlbum(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapCreate) {
		return
	}
	store, cleanup, ok := s.openAlbumStore(w, r, lib)
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
	a, err := store.Create(r.Context(), body.Name, body.Description)
	if err != nil {
		s.writeAlbumError(w, r, err)
		return
	}
	writeJSON(w, s.logger, http.StatusCreated, map[string]any{"album": toAlbumResponse(a)})
}

// handleUpdateAlbum — PATCH /api/v1/libraries/{id}/albums/{albumID}
//
// Renames an album, changes its description, or sets its cover. Every field is
// optional; an empty cover_file_id clears the cover.
func (s *Server) handleUpdateAlbum(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.requireCap(w, r, u, authz.EntityKey("a", lib.ID, r.PathValue("albumID")), authz.CapEdit) {
		return
	}
	store, cleanup, ok := s.openAlbumStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	var body struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
		CoverFileID *string `json:"cover_file_id"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), err)
		return
	}
	a, err := store.Update(r.Context(), r.PathValue("albumID"), albums.AlbumUpdate{
		Name:        body.Name,
		Description: body.Description,
		CoverFileID: body.CoverFileID,
	})
	if err != nil {
		s.writeAlbumError(w, r, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"album": toAlbumResponse(a)})
}

// handleDeleteAlbum — DELETE /api/v1/libraries/{id}/albums/{albumID}
func (s *Server) handleDeleteAlbum(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.requireCap(w, r, u, authz.EntityKey("a", lib.ID, r.PathValue("albumID")), authz.CapDelete) {
		return
	}
	store, cleanup, ok := s.openAlbumStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	if err := store.Delete(r.Context(), r.PathValue("albumID")); err != nil {
		s.writeAlbumError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleAddAlbumFile — POST /api/v1/libraries/{id}/albums/{albumID}/files/{fileID}
func (s *Server) handleAddAlbumFile(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.requireCap(w, r, u, authz.EntityKey("a", lib.ID, r.PathValue("albumID")), authz.CapEdit) {
		return
	}
	store, cleanup, ok := s.openAlbumStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	if err := store.AddFile(r.Context(), r.PathValue("albumID"), r.PathValue("fileID")); err != nil {
		s.writeAlbumError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRemoveAlbumFile — DELETE /api/v1/libraries/{id}/albums/{albumID}/files/{fileID}
func (s *Server) handleRemoveAlbumFile(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.requireCap(w, r, u, authz.EntityKey("a", lib.ID, r.PathValue("albumID")), authz.CapEdit) {
		return
	}
	store, cleanup, ok := s.openAlbumStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	if err := store.RemoveFile(r.Context(), r.PathValue("albumID"), r.PathValue("fileID")); err != nil {
		s.writeAlbumError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleListAlbumFiles — GET /api/v1/libraries/{id}/albums/{albumID}/files
func (s *Server) handleListAlbumFiles(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.requireCap(w, r, u, authz.EntityKey("a", lib.ID, r.PathValue("albumID")), authz.CapRead) {
		return
	}
	store, cleanup, ok := s.openAlbumStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	files, err := store.ListFiles(r.Context(), r.PathValue("albumID"))
	if err != nil {
		s.logger.Error("list album files", "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to list album files.")
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"files": toFileResponses(files)})
}

// writeAlbumError maps album domain errors to HTTP responses.
func (s *Server) writeAlbumError(w http.ResponseWriter, r *http.Request, err error) {
	reqID := requestIDOrEmpty(r)
	switch {
	case errors.Is(err, albums.ErrNotFound):
		writeError(w, s.logger, reqID, http.StatusNotFound, CodeNotFound, "Album not found.")
	case errors.Is(err, albums.ErrFileNotInAlbum):
		writeError(w, s.logger, reqID, http.StatusNotFound, CodeNotFound,
			"File is not in this album.")
	case errors.Is(err, albums.ErrEmptyName):
		writeError(w, s.logger, reqID, http.StatusBadRequest, CodeBadRequest,
			"An album needs a name.")
	default:
		s.logger.Error("album operation", "error", err)
		writeError(w, s.logger, reqID, http.StatusInternalServerError,
			CodeInternal, "Internal server error.")
	}
}
