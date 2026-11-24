package httpapi

import (
	"net/http"
	"strings"

	"github.com/Jishnu-Prasad888/Orchard/internal/auth"
)

// hiddenCollectionLibrariesKey is the user_settings key for the libraries
// whose collections the user keeps off the Collections page and Home (a
// search still finds them). It is the user's choice, not the library's, so
// it lives with the account rather than in any library's database.
const hiddenCollectionLibrariesKey = "collections.hidden_libraries"

// maxHiddenLibraries bounds the stored list; nobody has this many libraries.
const maxHiddenLibraries = 1000

type hiddenLibrariesBody struct {
	LibraryIDs []string `json:"library_ids"`
}

// handleGetHiddenCollectionLibraries — GET /api/v1/settings/hidden-collection-libraries
func (s *Server) handleGetHiddenCollectionLibraries(w http.ResponseWriter, r *http.Request, u *auth.User) {
	body := hiddenLibrariesBody{LibraryIDs: []string{}}
	if s.userSettings != nil {
		if _, err := s.userSettings.GetJSON(r.Context(), u.ID, hiddenCollectionLibrariesKey, &body.LibraryIDs); err != nil {
			s.logger.Error("get hidden collection libraries", "error", err)
			writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
				CodeInternal, "Internal server error.")
			return
		}
	}
	if body.LibraryIDs == nil {
		body.LibraryIDs = []string{}
	}
	writeJSON(w, s.logger, http.StatusOK, body)
}

// handleSetHiddenCollectionLibraries — PUT /api/v1/settings/hidden-collection-libraries
// The list is replaced whole; blanks and repeats are dropped.
func (s *Server) handleSetHiddenCollectionLibraries(w http.ResponseWriter, r *http.Request, u *auth.User) {
	reqID := requestIDOrEmpty(r)
	var body hiddenLibrariesBody
	if err := readJSON(w, r, &body); err != nil {
		writeDomainError(w, s.logger, reqID, err)
		return
	}
	if len(body.LibraryIDs) > maxHiddenLibraries {
		writeError(w, s.logger, reqID, http.StatusBadRequest, CodeBadRequest, "Too many libraries.")
		return
	}
	if s.userSettings == nil {
		writeError(w, s.logger, reqID, http.StatusServiceUnavailable, CodeServiceUnavailable,
			"Settings are not available.")
		return
	}
	seen := make(map[string]bool, len(body.LibraryIDs))
	ids := make([]string, 0, len(body.LibraryIDs))
	for _, id := range body.LibraryIDs {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	if err := s.userSettings.SetJSON(r.Context(), u.ID, hiddenCollectionLibrariesKey, ids); err != nil {
		s.logger.Error("set hidden collection libraries", "error", err)
		writeError(w, s.logger, reqID, http.StatusInternalServerError, CodeInternal, "Internal server error.")
		return
	}
	writeJSON(w, s.logger, http.StatusOK, hiddenLibrariesBody{LibraryIDs: ids})
}
