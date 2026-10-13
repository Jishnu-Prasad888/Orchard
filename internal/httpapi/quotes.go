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
	"github.com/Jishnu-Prasad888/Orchard/internal/quotes"
)

// maxQuoteListLimit bounds a caller-supplied ?limit= on the library-wide
// quotes listing (e.g. Home's "recent quotes" section).
const maxQuoteListLimit = 100

// openQuoteStore opens the per-library DB and returns a quotes.Store.
func (s *Server) openQuoteStore(
	w http.ResponseWriter, r *http.Request, lib *library.Library,
) (*quotes.Store, func(), bool) {
	if lib.Status == library.StatusOffline {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusConflict,
			CodeConflict, "Library is offline.")
		return nil, nil, false
	}
	db, err := librarydb.Open(lib.Root + "/.orchard")
	if err != nil {
		s.logger.Error("open library db for quotes", "library_id", lib.ID, "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to open library database.")
		return nil, nil, false
	}
	return quotes.NewStore(db), func() { _ = db.Close() }, true
}

// quoteResponse is the API representation of a quote.
type quoteResponse struct {
	ID         string `json:"id"`
	FileID     string `json:"file_id"`
	Text       string `json:"text"`
	Location   string `json:"location,omitempty"`
	Author     string `json:"author,omitempty"`
	Chapter    string `json:"chapter,omitempty"`
	Page       string `json:"page,omitempty"`
	Commentary string `json:"commentary,omitempty"`
	Favorite   bool   `json:"favorite"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

func toQuoteResponse(q *quotes.Quote) quoteResponse {
	return quoteResponse{
		ID:         q.ID,
		FileID:     q.FileID,
		Text:       q.Text,
		Location:   q.Location,
		Author:     q.Author,
		Chapter:    q.Chapter,
		Page:       q.Page,
		Commentary: q.Commentary,
		Favorite:   q.Favorite,
		CreatedAt:  q.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:  q.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func toQuoteResponses(qs []*quotes.Quote) []quoteResponse {
	out := make([]quoteResponse, 0, len(qs))
	for _, q := range qs {
		out = append(out, toQuoteResponse(q))
	}
	return out
}

// handleListLibraryQuotes — GET /api/v1/libraries/{id}/quotes?favorite=1
func (s *Server) handleListLibraryQuotes(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapRead) {
		return
	}
	store, cleanup, ok := s.openQuoteStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	limit := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = min(n, maxQuoteListLimit)
		}
	}
	qs, err := store.List(r.Context(), u.ID, quotes.ListOptions{
		FavoriteOnly: r.URL.Query().Get("favorite") == "1",
		Limit:        limit,
	})
	if err != nil {
		s.logger.Error("list quotes", "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to list quotes.")
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"quotes": toQuoteResponses(qs)})
}

// handleListFileQuotes — GET /api/v1/libraries/{id}/files/{fileID}/quotes
func (s *Server) handleListFileQuotes(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.fileKeyFor(w, r, u, lib, r.PathValue("fileID"), authz.CapRead) {
		return
	}
	store, cleanup, ok := s.openQuoteStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	qs, err := store.List(r.Context(), u.ID, quotes.ListOptions{FileID: r.PathValue("fileID")})
	if err != nil {
		s.logger.Error("list file quotes", "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to list quotes.")
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"quotes": toQuoteResponses(qs)})
}

// handleCreateQuote — POST /api/v1/libraries/{id}/files/{fileID}/quotes
func (s *Server) handleCreateQuote(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.fileKeyFor(w, r, u, lib, r.PathValue("fileID"), authz.CapEdit) {
		return
	}
	var body struct {
		Text       string `json:"text"`
		Location   string `json:"location"`
		Author     string `json:"author"`
		Chapter    string `json:"chapter"`
		Page       string `json:"page"`
		Commentary string `json:"commentary"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), err)
		return
	}
	if body.Text == "" {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusBadRequest,
			CodeBadRequest, "text is required.")
		return
	}
	store, cleanup, ok := s.openQuoteStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	q, err := store.Create(r.Context(), u.ID, r.PathValue("fileID"), quotes.Fields{
		Text:       body.Text,
		Location:   body.Location,
		Author:     body.Author,
		Chapter:    body.Chapter,
		Page:       body.Page,
		Commentary: body.Commentary,
	})
	if err != nil {
		s.writeQuoteError(w, r, err)
		return
	}
	writeJSON(w, s.logger, http.StatusCreated, map[string]any{"quote": toQuoteResponse(q)})
}

// handleUpdateQuote — PATCH /api/v1/libraries/{id}/quotes/{quoteID}
func (s *Server) handleUpdateQuote(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapEdit) {
		return
	}
	var body struct {
		Text       *string `json:"text"`
		Author     *string `json:"author"`
		Chapter    *string `json:"chapter"`
		Page       *string `json:"page"`
		Commentary *string `json:"commentary"`
		Favorite   *bool   `json:"favorite"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), err)
		return
	}
	store, cleanup, ok := s.openQuoteStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	q, err := store.Update(r.Context(), r.PathValue("quoteID"), u.ID, quotes.Update{
		Text:       body.Text,
		Author:     body.Author,
		Chapter:    body.Chapter,
		Page:       body.Page,
		Commentary: body.Commentary,
		Favorite:   body.Favorite,
	})
	if err != nil {
		s.writeQuoteError(w, r, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"quote": toQuoteResponse(q)})
}

// handleDeleteQuote — DELETE /api/v1/libraries/{id}/quotes/{quoteID}
func (s *Server) handleDeleteQuote(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapDelete) {
		return
	}
	store, cleanup, ok := s.openQuoteStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	if err := store.Delete(r.Context(), r.PathValue("quoteID"), u.ID); err != nil {
		s.writeQuoteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeQuoteError maps quote domain errors to HTTP responses.
func (s *Server) writeQuoteError(w http.ResponseWriter, r *http.Request, err error) {
	reqID := requestIDOrEmpty(r)
	switch {
	case errors.Is(err, quotes.ErrNotFound):
		writeError(w, s.logger, reqID, http.StatusNotFound, CodeNotFound, "Quote not found.")
	default:
		s.logger.Error("quote operation", "error", err)
		writeError(w, s.logger, reqID, http.StatusInternalServerError,
			CodeInternal, "Internal server error.")
	}
}
