package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/Jishnu-Prasad888/Orchard/internal/auth"
	"github.com/Jishnu-Prasad888/Orchard/internal/dictionary"
)

// dictionaryResponse is the API representation of an installed dictionary.
type dictionaryResponse struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version"`
	License     string `json:"license"`
	SizeBytes   int64  `json:"size_bytes"`
	InstalledAt string `json:"installed_at"`
	Enabled     bool   `json:"enabled"`
}

func toDictionaryResponse(d dictionary.Installed) dictionaryResponse {
	return dictionaryResponse{
		ID:          d.ID,
		Kind:        d.Kind,
		Name:        d.Name,
		Description: d.Description,
		Version:     d.Version,
		License:     d.License,
		SizeBytes:   d.SizeBytes,
		InstalledAt: d.InstalledAt.UTC().Format(time.RFC3339),
		Enabled:     d.Enabled,
	}
}

// handleListDictionaries — GET /api/v1/dictionaries
//
// Open to every session: the reader asks it whether to offer "Search in
// dictionary" at all. The install job rides along so the settings page can
// pick a running download back up after a reload.
func (s *Server) handleListDictionaries(w http.ResponseWriter, _ *http.Request, _ *auth.User) {
	installed := s.dictionaries.Installed()
	out := make([]dictionaryResponse, 0, len(installed))
	for _, d := range installed {
		out = append(out, toDictionaryResponse(d))
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{
		"dictionaries": out,
		"available":    s.dictionaries.Available(),
		"job":          s.dictionaries.Job(),
	})
}

// handleDictionaryCatalog — GET /api/v1/dictionaries/catalog (admin)
func (s *Server) handleDictionaryCatalog(w http.ResponseWriter, r *http.Request, _ *auth.User) {
	sources, err := s.dictionaries.Catalog(r.Context())
	resp := map[string]any{"sources": sources}
	if err != nil {
		// WordNet is still offered; the page says why FreeDict isn't.
		resp["freedict_error"] = err.Error()
	}
	writeJSON(w, s.logger, http.StatusOK, resp)
}

// handleInstallDictionary — POST /api/v1/dictionaries/install (admin)
func (s *Server) handleInstallDictionary(w http.ResponseWriter, r *http.Request, _ *auth.User) {
	var body struct {
		ID string `json:"id"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), err)
		return
	}
	job, err := s.dictionaries.Install(r.Context(), body.ID)
	if err != nil {
		s.writeDictionaryError(w, r, err)
		return
	}
	writeJSON(w, s.logger, http.StatusAccepted, map[string]any{"job": job})
}

// handleCancelDictionaryInstall — DELETE /api/v1/dictionaries/install (admin)
func (s *Server) handleCancelDictionaryInstall(w http.ResponseWriter, r *http.Request, _ *auth.User) {
	if err := s.dictionaries.Cancel(); err != nil {
		s.writeDictionaryError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleUpdateDictionary — PATCH /api/v1/dictionaries/{dictID} (admin)
func (s *Server) handleUpdateDictionary(w http.ResponseWriter, r *http.Request, _ *auth.User) {
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), err)
		return
	}
	if body.Enabled == nil {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusBadRequest, CodeBadRequest,
			"enabled is required.")
		return
	}
	d, err := s.dictionaries.SetEnabled(r.PathValue("dictID"), *body.Enabled)
	if err != nil {
		s.writeDictionaryError(w, r, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"dictionary": toDictionaryResponse(d)})
}

// handleDeleteDictionary — DELETE /api/v1/dictionaries/{dictID} (admin)
func (s *Server) handleDeleteDictionary(w http.ResponseWriter, r *http.Request, _ *auth.User) {
	if err := s.dictionaries.Remove(r.PathValue("dictID")); err != nil {
		s.writeDictionaryError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDictionaryLookup — GET /api/v1/dictionaries/lookup?word=<word>
func (s *Server) handleDictionaryLookup(w http.ResponseWriter, r *http.Request, _ *auth.User) {
	word, results, err := s.dictionaries.Lookup(r.URL.Query().Get("word"))
	if err != nil {
		s.writeDictionaryError(w, r, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"word": word, "results": results})
}

// writeDictionaryError maps dictionary errors to HTTP responses.
func (s *Server) writeDictionaryError(w http.ResponseWriter, r *http.Request, err error) {
	reqID := requestIDOrEmpty(r)
	switch {
	case errors.Is(err, dictionary.ErrNotFound):
		writeError(w, s.logger, reqID, http.StatusNotFound, CodeNotFound, "That dictionary isn't installed.")
	case errors.Is(err, dictionary.ErrUnknownSource):
		writeError(w, s.logger, reqID, http.StatusNotFound, CodeNotFound, "There is no such dictionary to install.")
	case errors.Is(err, dictionary.ErrBusy):
		writeError(w, s.logger, reqID, http.StatusConflict, CodeConflict,
			"Another dictionary is being installed. Wait for it to finish.")
	case errors.Is(err, dictionary.ErrInstalled):
		writeError(w, s.logger, reqID, http.StatusConflict, CodeConflict, "That dictionary is already installed.")
	case errors.Is(err, dictionary.ErrNoJob):
		writeError(w, s.logger, reqID, http.StatusConflict, CodeConflict, "No dictionary is being installed.")
	case errors.Is(err, dictionary.ErrBadWord):
		writeError(w, s.logger, reqID, http.StatusBadRequest, CodeBadRequest,
			"Select a single word or a short phrase to look up.")
	default:
		// The catalog couldn't be fetched, or a disk error: the message
		// is written for people and says which.
		s.logger.Error("dictionary operation", "error", err)
		writeError(w, s.logger, reqID, http.StatusBadGateway, CodeServiceUnavailable, err.Error())
	}
}
