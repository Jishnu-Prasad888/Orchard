package httpapi

import (
	"net/http"
	"time"

	"github.com/Jishnu-Prasad888/Cairn/internal/auth"
	"github.com/Jishnu-Prasad888/Cairn/internal/authz"
	"github.com/Jishnu-Prasad888/Cairn/internal/library"
	"github.com/Jishnu-Prasad888/Cairn/internal/librarydb"
	"github.com/Jishnu-Prasad888/Cairn/internal/notes"
)

// maxNoteBodyBytes bounds a single note's Markdown body. Captions and short
// descriptions are the intended use; anything longer belongs in a memory.
const maxNoteBodyBytes = 256 << 10 // 256 KiB

// openNoteStore opens the per-library DB and returns a NoteStore.
func (s *Server) openNoteStore(
	w http.ResponseWriter, r *http.Request, lib *library.Library,
) (*notes.NoteStore, func(), bool) {
	if lib.Status == library.StatusOffline {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusConflict,
			CodeConflict, "Library is offline.")
		return nil, nil, false
	}
	cairnDir := lib.Root + "/.cairn"
	db, err := librarydb.Open(cairnDir)
	if err != nil {
		s.logger.Error("open library db for notes", "library_id", lib.ID, "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to open library database.")
		return nil, nil, false
	}
	return notes.NewNoteStore(db, lib.ID), func() { _ = db.Close() }, true
}

// handleGetFileNote — GET /api/v1/libraries/{id}/files/{fileID}/note
// Returns the file's Markdown note (an empty body when none exists yet).
func (s *Server) handleGetFileNote(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.fileKeyFor(w, r, u, lib, r.PathValue("fileID"), authz.CapRead) {
		return
	}
	store, cleanup, ok := s.openNoteStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	n, err := store.Get(r.Context(), r.PathValue("fileID"))
	if err != nil {
		s.logger.Error("get file note", "library_id", lib.ID, "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to read the file note.")
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"note": toNoteResponse(n)})
}

// handleSetFileNote — PUT /api/v1/libraries/{id}/files/{fileID}/note
// Replaces the file's Markdown note. An empty body clears the note.
func (s *Server) handleSetFileNote(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.fileKeyFor(w, r, u, lib, r.PathValue("fileID"), authz.CapEdit) {
		return
	}

	var body struct {
		Body string `json:"body"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), err)
		return
	}
	if len(body.Body) > maxNoteBodyBytes {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusRequestEntityTooLarge,
			CodePayloadTooLarge, "Note is too large. Keep it under 256 KiB.")
		return
	}

	store, cleanup, ok := s.openNoteStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	n, err := store.Set(r.Context(), r.PathValue("fileID"), body.Body)
	if err != nil {
		s.logger.Error("set file note", "library_id", lib.ID, "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to save the file note.")
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"note": toNoteResponse(n)})
}

// handleClearFileNote — DELETE /api/v1/libraries/{id}/files/{fileID}/note
// Removes the file's note entirely.
func (s *Server) handleClearFileNote(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.fileKeyFor(w, r, u, lib, r.PathValue("fileID"), authz.CapEdit) {
		return
	}
	store, cleanup, ok := s.openNoteStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	if err := store.Clear(r.Context(), r.PathValue("fileID")); err != nil {
		s.logger.Error("clear file note", "library_id", lib.ID, "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to clear the file note.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// noteResponse is the wire representation of a per-file note.
type noteResponse struct {
	FileID    string `json:"file_id"`
	Body      string `json:"body"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

func toNoteResponse(n notes.Note) noteResponse {
	updated := ""
	if !n.UpdatedAt.IsZero() {
		updated = n.UpdatedAt.UTC().Format(time.RFC3339)
	}
	return noteResponse{FileID: n.FileID, Body: n.Body, UpdatedAt: updated}
}
