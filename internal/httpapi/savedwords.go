package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/Jishnu-Prasad888/Orchard/internal/auth"
	"github.com/Jishnu-Prasad888/Orchard/internal/authz"
	"github.com/Jishnu-Prasad888/Orchard/internal/library"
	"github.com/Jishnu-Prasad888/Orchard/internal/librarydb"
	"github.com/Jishnu-Prasad888/Orchard/internal/savedwords"
)

// openSavedWordStore opens the per-library DB and returns a savedwords.Store.
func (s *Server) openSavedWordStore(
	w http.ResponseWriter, r *http.Request, lib *library.Library,
) (*savedwords.Store, func(), bool) {
	if lib.Status == library.StatusOffline {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusConflict,
			CodeConflict, "Library is offline.")
		return nil, nil, false
	}
	db, release, err := librarydb.Acquire(lib.Root + "/.orchard")
	if err != nil {
		s.logger.Error("open library db for saved words", "library_id", lib.ID, "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to open library database.")
		return nil, nil, false
	}
	return savedwords.NewStore(db), release, true
}

// savedWordResponse is the API representation of a saved word.
type savedWordResponse struct {
	ID         string `json:"id"`
	FileID     string `json:"file_id"`
	Word       string `json:"word"`
	Location   string `json:"location"`
	Text       string `json:"text"`
	Definition string `json:"definition"`
	Source     string `json:"source,omitempty"`
	CreatedAt  string `json:"created_at"`
}

func toSavedWordResponse(sw *savedwords.Word) savedWordResponse {
	return savedWordResponse{
		ID:         sw.ID,
		FileID:     sw.FileID,
		Word:       sw.Word,
		Location:   sw.Location,
		Text:       sw.Text,
		Definition: sw.Definition,
		Source:     sw.Source,
		CreatedAt:  sw.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// handleListFileWords — GET /api/v1/libraries/{id}/files/{fileID}/words
func (s *Server) handleListFileWords(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.fileKeyFor(w, r, u, lib, r.PathValue("fileID"), authz.CapRead) {
		return
	}
	store, cleanup, ok := s.openSavedWordStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	words, err := store.ListByFile(r.Context(), u.ID, r.PathValue("fileID"))
	if err != nil {
		s.writeSavedWordError(w, r, err)
		return
	}
	out := make([]savedWordResponse, 0, len(words))
	for _, sw := range words {
		out = append(out, toSavedWordResponse(sw))
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"words": out})
}

// handleCreateWord — POST /api/v1/libraries/{id}/files/{fileID}/words
func (s *Server) handleCreateWord(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.fileKeyFor(w, r, u, lib, r.PathValue("fileID"), authz.CapEdit) {
		return
	}
	var body struct {
		Word       string `json:"word"`
		Location   string `json:"location"`
		Text       string `json:"text"`
		Definition string `json:"definition"`
		Source     string `json:"source"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), err)
		return
	}
	store, cleanup, ok := s.openSavedWordStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	sw, err := store.Create(r.Context(), savedwords.Word{
		UserID:     u.ID,
		FileID:     r.PathValue("fileID"),
		Word:       body.Word,
		Location:   body.Location,
		Text:       body.Text,
		Definition: body.Definition,
		Source:     body.Source,
	})
	if err != nil {
		s.writeSavedWordError(w, r, err)
		return
	}
	writeJSON(w, s.logger, http.StatusCreated, map[string]any{"word": toSavedWordResponse(sw)})
}

// handleDeleteWord — DELETE /api/v1/libraries/{id}/words/{wordID}
func (s *Server) handleDeleteWord(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapDelete) {
		return
	}
	store, cleanup, ok := s.openSavedWordStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	if err := store.Delete(r.Context(), r.PathValue("wordID"), u.ID); err != nil {
		s.writeSavedWordError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeSavedWordError maps saved-word errors to HTTP responses.
func (s *Server) writeSavedWordError(w http.ResponseWriter, r *http.Request, err error) {
	reqID := requestIDOrEmpty(r)
	switch {
	case errors.Is(err, savedwords.ErrNotFound):
		writeError(w, s.logger, reqID, http.StatusNotFound, CodeNotFound, "Saved word not found.")
	case errors.Is(err, savedwords.ErrInvalid):
		writeError(w, s.logger, reqID, http.StatusBadRequest, CodeBadRequest,
			"A saved word needs a word (up to 64 characters) and a place in the book.")
	default:
		s.logger.Error("saved word operation", "error", err)
		writeError(w, s.logger, reqID, http.StatusInternalServerError,
			CodeInternal, "Internal server error.")
	}
}
