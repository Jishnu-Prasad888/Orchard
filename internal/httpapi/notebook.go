package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/Jishnu-Prasad888/Orchard/internal/auth"
	"github.com/Jishnu-Prasad888/Orchard/internal/authz"
	"github.com/Jishnu-Prasad888/Orchard/internal/library"
	"github.com/Jishnu-Prasad888/Orchard/internal/librarydb"
	"github.com/Jishnu-Prasad888/Orchard/internal/notebook"
)

// maxNoteListLimit bounds a caller-supplied ?limit= on the library-wide
// notes listing (e.g. Home's "recent notes" section).
const maxNoteListLimit = 100

// openNotebookStore opens the per-library DB and returns a notebook.Store.
func (s *Server) openNotebookStore(
	w http.ResponseWriter, r *http.Request, lib *library.Library,
) (*notebook.Store, func(), bool) {
	if lib.Status == library.StatusOffline {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusConflict,
			CodeConflict, "Library is offline.")
		return nil, nil, false
	}
	db, err := librarydb.Open(lib.Root + "/.orchard")
	if err != nil {
		s.logger.Error("open library db for notes", "library_id", lib.ID, "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to open library database.")
		return nil, nil, false
	}
	return notebook.NewStore(db), func() { _ = db.Close() }, true
}

// notebookNoteResponse is the API representation of a note.
type notebookNoteResponse struct {
	ID          string `json:"id"`
	FileID      string `json:"file_id,omitempty"`
	QuoteID     string `json:"quote_id,omitempty"`
	HighlightID string `json:"highlight_id,omitempty"`
	Title       string `json:"title,omitempty"`
	Body        string `json:"body"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

func toNotebookNoteResponse(n *notebook.Note) notebookNoteResponse {
	return notebookNoteResponse{
		ID:          n.ID,
		FileID:      n.FileID,
		QuoteID:     n.QuoteID,
		HighlightID: n.HighlightID,
		Title:       n.Title,
		Body:        n.Body,
		CreatedAt:   n.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:   n.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func toNotebookNoteResponses(ns []*notebook.Note) []notebookNoteResponse {
	out := make([]notebookNoteResponse, 0, len(ns))
	for _, n := range ns {
		out = append(out, toNotebookNoteResponse(n))
	}
	return out
}

// handleListNotes — GET /api/v1/libraries/{id}/notes?file_id=&tag_id=&standalone=1&limit=N
func (s *Server) handleListNotes(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapRead) {
		return
	}
	store, cleanup, ok := s.openNotebookStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	q := r.URL.Query()
	limit := 0
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = min(n, maxNoteListLimit)
		}
	}
	ns, err := store.List(r.Context(), u.ID, notebook.ListOptions{
		FileID:     q.Get("file_id"),
		TagID:      q.Get("tag_id"),
		Standalone: q.Get("standalone") == "1",
		Limit:      limit,
	})
	if err != nil {
		s.logger.Error("list notes", "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to list notes.")
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"notes": toNotebookNoteResponses(ns)})
}

// handleListFileNotes — GET /api/v1/libraries/{id}/files/{fileID}/notes
func (s *Server) handleListFileNotes(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.fileKeyFor(w, r, u, lib, r.PathValue("fileID"), authz.CapRead) {
		return
	}
	store, cleanup, ok := s.openNotebookStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	ns, err := store.List(r.Context(), u.ID, notebook.ListOptions{FileID: r.PathValue("fileID")})
	if err != nil {
		s.logger.Error("list file notes", "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to list notes.")
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"notes": toNotebookNoteResponses(ns)})
}

// handleCreateNote — POST /api/v1/libraries/{id}/notes
func (s *Server) handleCreateNote(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapCreate) {
		return
	}
	var body struct {
		Title       string `json:"title"`
		Body        string `json:"body"`
		FileID      string `json:"file_id"`
		QuoteID     string `json:"quote_id"`
		HighlightID string `json:"highlight_id"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), err)
		return
	}
	store, cleanup, ok := s.openNotebookStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	n, err := store.Create(r.Context(), u.ID, notebook.Fields{
		Title:       body.Title,
		Body:        body.Body,
		FileID:      body.FileID,
		QuoteID:     body.QuoteID,
		HighlightID: body.HighlightID,
	})
	if err != nil {
		s.writeNoteError(w, r, err)
		return
	}
	writeJSON(w, s.logger, http.StatusCreated, map[string]any{"note": toNotebookNoteResponse(n)})
}

// handleGetNote — GET /api/v1/libraries/{id}/notes/{noteID}
func (s *Server) handleGetNote(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapRead) {
		return
	}
	store, cleanup, ok := s.openNotebookStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	n, err := store.GetOwned(r.Context(), r.PathValue("noteID"), u.ID)
	if err != nil {
		s.writeNoteError(w, r, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"note": toNotebookNoteResponse(n)})
}

// handleUpdateNote — PATCH /api/v1/libraries/{id}/notes/{noteID}
func (s *Server) handleUpdateNote(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapEdit) {
		return
	}
	var body struct {
		Title *string `json:"title"`
		Body  *string `json:"body"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), err)
		return
	}
	store, cleanup, ok := s.openNotebookStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	n, err := store.Update(r.Context(), r.PathValue("noteID"), u.ID, notebook.Update{
		Title: body.Title,
		Body:  body.Body,
	})
	if err != nil {
		s.writeNoteError(w, r, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"note": toNotebookNoteResponse(n)})
}

// handleDeleteNote — DELETE /api/v1/libraries/{id}/notes/{noteID}
func (s *Server) handleDeleteNote(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapDelete) {
		return
	}
	store, cleanup, ok := s.openNotebookStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	if err := store.Delete(r.Context(), r.PathValue("noteID"), u.ID); err != nil {
		s.writeNoteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleListNoteTags — GET /api/v1/libraries/{id}/notes/{noteID}/tags
func (s *Server) handleListNoteTags(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapRead) {
		return
	}
	noteStore, cleanup, ok := s.openNotebookStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()
	if _, err := noteStore.GetOwned(r.Context(), r.PathValue("noteID"), u.ID); err != nil {
		s.writeNoteError(w, r, err)
		return
	}

	tagStore, cleanup2, ok := s.openTagStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup2()

	ts, err := tagStore.ListByNote(r.Context(), r.PathValue("noteID"))
	if err != nil {
		s.logger.Error("list note tags", "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to list tags.")
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"tags": toTagResponses(ts)})
}

// handleAddNoteTag — POST /api/v1/libraries/{id}/notes/{noteID}/tags
func (s *Server) handleAddNoteTag(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapEdit) {
		return
	}
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
	noteStore, cleanup, ok := s.openNotebookStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()
	if _, err := noteStore.GetOwned(r.Context(), r.PathValue("noteID"), u.ID); err != nil {
		s.writeNoteError(w, r, err)
		return
	}

	tagStore, cleanup2, ok := s.openTagStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup2()

	if err := tagStore.AttachToNote(r.Context(), r.PathValue("noteID"), body.TagID); err != nil {
		s.writeTagError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRemoveNoteTag — DELETE /api/v1/libraries/{id}/notes/{noteID}/tags/{tagID}
func (s *Server) handleRemoveNoteTag(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapEdit) {
		return
	}
	noteStore, cleanup, ok := s.openNotebookStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()
	if _, err := noteStore.GetOwned(r.Context(), r.PathValue("noteID"), u.ID); err != nil {
		s.writeNoteError(w, r, err)
		return
	}

	tagStore, cleanup2, ok := s.openTagStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup2()

	if err := tagStore.DetachFromNote(r.Context(), r.PathValue("noteID"), r.PathValue("tagID")); err != nil {
		s.writeTagError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleListNoteLinks — GET /api/v1/libraries/{id}/notes/{noteID}/links
func (s *Server) handleListNoteLinks(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapRead) {
		return
	}
	store, cleanup, ok := s.openNotebookStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	ns, err := store.ListLinked(r.Context(), u.ID, r.PathValue("noteID"))
	if err != nil {
		s.writeNoteError(w, r, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"notes": toNotebookNoteResponses(ns)})
}

// handleAddNoteLink — POST /api/v1/libraries/{id}/notes/{noteID}/links
func (s *Server) handleAddNoteLink(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapEdit) {
		return
	}
	var body struct {
		LinkedNoteID string `json:"linked_note_id"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), err)
		return
	}
	if body.LinkedNoteID == "" {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusBadRequest,
			CodeBadRequest, "linked_note_id is required.")
		return
	}
	store, cleanup, ok := s.openNotebookStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	if err := store.Link(r.Context(), u.ID, r.PathValue("noteID"), body.LinkedNoteID); err != nil {
		s.writeNoteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRemoveNoteLink — DELETE /api/v1/libraries/{id}/notes/{noteID}/links/{linkedNoteID}
func (s *Server) handleRemoveNoteLink(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapEdit) {
		return
	}
	store, cleanup, ok := s.openNotebookStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	if err := store.Unlink(r.Context(), u.ID, r.PathValue("noteID"), r.PathValue("linkedNoteID")); err != nil {
		s.writeNoteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeNoteError maps note domain errors to HTTP responses.
func (s *Server) writeNoteError(w http.ResponseWriter, r *http.Request, err error) {
	reqID := requestIDOrEmpty(r)
	switch {
	case errors.Is(err, notebook.ErrNotFound):
		writeError(w, s.logger, reqID, http.StatusNotFound, CodeNotFound, "Note not found.")
	default:
		s.logger.Error("note operation", "error", err)
		writeError(w, s.logger, reqID, http.StatusInternalServerError,
			CodeInternal, "Internal server error.")
	}
}
