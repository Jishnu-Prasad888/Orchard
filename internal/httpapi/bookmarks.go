package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/Jishnu-Prasad888/Orchard/internal/auth"
	"github.com/Jishnu-Prasad888/Orchard/internal/authz"
	"github.com/Jishnu-Prasad888/Orchard/internal/bookmarks"
	"github.com/Jishnu-Prasad888/Orchard/internal/library"
	"github.com/Jishnu-Prasad888/Orchard/internal/librarydb"
)

// openBookmarkStore opens the per-library DB and returns a bookmarks.Store.
func (s *Server) openBookmarkStore(
	w http.ResponseWriter, r *http.Request, lib *library.Library,
) (*bookmarks.Store, func(), bool) {
	if lib.Status == library.StatusOffline {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusConflict,
			CodeConflict, "Library is offline.")
		return nil, nil, false
	}
	db, release, err := librarydb.Acquire(lib.Root + "/.orchard")
	if err != nil {
		s.logger.Error("open library db for bookmarks", "library_id", lib.ID, "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to open library database.")
		return nil, nil, false
	}
	return bookmarks.NewStore(db), release, true
}

// bookmarkResponse is the API representation of a bookmark.
type bookmarkResponse struct {
	ID        string `json:"id"`
	FileID    string `json:"file_id"`
	Location  string `json:"location"`
	Label     string `json:"label,omitempty"`
	CreatedAt string `json:"created_at"`
}

func toBookmarkResponse(b *bookmarks.Bookmark) bookmarkResponse {
	return bookmarkResponse{
		ID:        b.ID,
		FileID:    b.FileID,
		Location:  b.Location,
		Label:     b.Label,
		CreatedAt: b.CreatedAt.UTC().Format(time.RFC3339),
	}
}

func toBookmarkResponses(bs []*bookmarks.Bookmark) []bookmarkResponse {
	out := make([]bookmarkResponse, 0, len(bs))
	for _, b := range bs {
		out = append(out, toBookmarkResponse(b))
	}
	return out
}

// handleListLibraryBookmarks — GET /api/v1/libraries/{id}/bookmarks
func (s *Server) handleListLibraryBookmarks(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapRead) {
		return
	}
	store, cleanup, ok := s.openBookmarkStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	bs, err := store.List(r.Context(), u.ID)
	if err != nil {
		s.logger.Error("list bookmarks", "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to list bookmarks.")
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"bookmarks": toBookmarkResponses(bs)})
}

// handleListFileBookmarks — GET /api/v1/libraries/{id}/files/{fileID}/bookmarks
func (s *Server) handleListFileBookmarks(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.fileKeyFor(w, r, u, lib, r.PathValue("fileID"), authz.CapRead) {
		return
	}
	store, cleanup, ok := s.openBookmarkStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	bs, err := store.ListByFile(r.Context(), u.ID, r.PathValue("fileID"))
	if err != nil {
		s.logger.Error("list file bookmarks", "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to list bookmarks.")
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"bookmarks": toBookmarkResponses(bs)})
}

// handleCreateBookmark — POST /api/v1/libraries/{id}/files/{fileID}/bookmarks
func (s *Server) handleCreateBookmark(w http.ResponseWriter, r *http.Request, u *auth.User) {
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
		Label    string `json:"label"`
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
	store, cleanup, ok := s.openBookmarkStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	b, err := store.Create(r.Context(), u.ID, r.PathValue("fileID"), body.Location, body.Label)
	if err != nil {
		s.writeBookmarkError(w, r, err)
		return
	}
	writeJSON(w, s.logger, http.StatusCreated, map[string]any{"bookmark": toBookmarkResponse(b)})
}

// handleUpdateBookmark — PATCH /api/v1/libraries/{id}/bookmarks/{bookmarkID}
func (s *Server) handleUpdateBookmark(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapEdit) {
		return
	}
	var body struct {
		Label string `json:"label"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), err)
		return
	}
	store, cleanup, ok := s.openBookmarkStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	b, err := store.UpdateLabel(r.Context(), r.PathValue("bookmarkID"), u.ID, body.Label)
	if err != nil {
		s.writeBookmarkError(w, r, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"bookmark": toBookmarkResponse(b)})
}

// handleDeleteBookmark — DELETE /api/v1/libraries/{id}/bookmarks/{bookmarkID}
func (s *Server) handleDeleteBookmark(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapDelete) {
		return
	}
	store, cleanup, ok := s.openBookmarkStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	if err := store.Delete(r.Context(), r.PathValue("bookmarkID"), u.ID); err != nil {
		s.writeBookmarkError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeBookmarkError maps bookmark domain errors to HTTP responses.
func (s *Server) writeBookmarkError(w http.ResponseWriter, r *http.Request, err error) {
	reqID := requestIDOrEmpty(r)
	switch {
	case errors.Is(err, bookmarks.ErrNotFound):
		writeError(w, s.logger, reqID, http.StatusNotFound, CodeNotFound, "Bookmark not found.")
	default:
		s.logger.Error("bookmark operation", "error", err)
		writeError(w, s.logger, reqID, http.StatusInternalServerError,
			CodeInternal, "Internal server error.")
	}
}
