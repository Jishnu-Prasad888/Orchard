package httpapi

import (
	"net/http"

	"github.com/Jishnu-Prasad888/Orchard/internal/auth"
)

// hideCompletedKey is the user_settings key for whether file listings leave
// out the files the user has marked completed. It is one account-wide
// choice: the checkbox is the same on every page and every library.
const hideCompletedKey = "listings.hide_completed"

type hideCompletedBody struct {
	HideCompleted bool `json:"hide_completed"`
}

// handleGetHideCompleted — GET /api/v1/settings/hide-completed
// A user who has never chosen sees completed files (false).
func (s *Server) handleGetHideCompleted(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var body hideCompletedBody
	if s.userSettings != nil {
		if _, err := s.userSettings.GetJSON(r.Context(), u.ID, hideCompletedKey, &body.HideCompleted); err != nil {
			s.logger.Error("get hide-completed setting", "error", err)
			writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
				CodeInternal, "Internal server error.")
			return
		}
	}
	writeJSON(w, s.logger, http.StatusOK, body)
}

// handleSetHideCompleted — PUT /api/v1/settings/hide-completed
func (s *Server) handleSetHideCompleted(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var body hideCompletedBody
	if err := readJSON(w, r, &body); err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), err)
		return
	}
	if s.userSettings == nil {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusServiceUnavailable, CodeServiceUnavailable,
			"Settings are not available.")
		return
	}
	if err := s.userSettings.SetJSON(r.Context(), u.ID, hideCompletedKey, body.HideCompleted); err != nil {
		s.logger.Error("set hide-completed setting", "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Internal server error.")
		return
	}
	writeJSON(w, s.logger, http.StatusOK, body)
}
