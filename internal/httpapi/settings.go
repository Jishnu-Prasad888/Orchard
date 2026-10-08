package httpapi

import (
	"errors"
	"net/http"

	"github.com/Jishnu-Prasad888/Cairn/internal/auth"
	"github.com/Jishnu-Prasad888/Cairn/internal/usersettings"
)

// handleGetMemorySettings — GET /api/v1/settings/memories
//
// Returns the caller's Memories preferences with defaults filled in.
func (s *Server) handleGetMemorySettings(w http.ResponseWriter, r *http.Request, u *auth.User) {
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"settings": s.memorySettingsFor(r.Context(), u)})
}

// handleUpdateMemorySettings — PATCH /api/v1/settings/memories
//
// Updates any subset of the caller's Memories preferences.
func (s *Server) handleUpdateMemorySettings(w http.ResponseWriter, r *http.Request, u *auth.User) {
	if s.db == nil {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusServiceUnavailable,
			CodeServiceUnavailable, "Settings storage is unavailable.")
		return
	}
	var patch usersettings.MemoryPatch
	if err := readJSON(w, r, &patch); err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), err)
		return
	}
	settings, err := usersettings.NewStore(s.db).UpdateMemory(r.Context(), u.ID, patch)
	var ve *usersettings.ValidationError
	switch {
	case errors.As(err, &ve):
		s.badRequest(w, r, ve.Error())
	case err != nil:
		s.logger.Error("update memory settings", "user_id", u.ID, "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to save settings.")
	default:
		writeJSON(w, s.logger, http.StatusOK, map[string]any{"settings": settings})
	}
}
