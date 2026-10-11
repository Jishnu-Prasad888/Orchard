package httpapi

import (
	"net/http"

	"github.com/Jishnu-Prasad888/Orchard/internal/auth"
)

// readerPreferencesKey is the user_settings key reader preferences are
// stored under, as one JSON blob rather than one row per field: the whole
// set is always read and written together.
const readerPreferencesKey = "reader.preferences"

// ReaderPreferences are a user's reading preferences, applied across every
// book they open (Phase 2 scope: user-level only, no per-book override).
type ReaderPreferences struct {
	FontSize     int     `json:"font_size"`
	LineSpacing  float64 `json:"line_spacing"`
	Margin       int     `json:"margin"`
	ReadingWidth int     `json:"reading_width"`
	Theme        string  `json:"theme"`
}

// defaultReaderPreferences is returned when a user has never saved any.
var defaultReaderPreferences = ReaderPreferences{
	FontSize:     18,
	LineSpacing:  1.5,
	Margin:       24,
	ReadingWidth: 720,
	Theme:        "light",
}

func validReaderTheme(theme string) bool {
	switch theme {
	case "light", "dark", "sepia":
		return true
	default:
		return false
	}
}

// handleGetReaderPreferences — GET /api/v1/settings/reader
func (s *Server) handleGetReaderPreferences(w http.ResponseWriter, r *http.Request, u *auth.User) {
	prefs := defaultReaderPreferences
	if s.userSettings != nil {
		if _, err := s.userSettings.GetJSON(r.Context(), u.ID, readerPreferencesKey, &prefs); err != nil {
			s.logger.Error("get reader preferences", "error", err)
			writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
				CodeInternal, "Internal server error.")
			return
		}
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"preferences": prefs})
}

// handleSetReaderPreferences — PUT /api/v1/settings/reader
func (s *Server) handleSetReaderPreferences(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var body ReaderPreferences
	if err := readJSON(w, r, &body); err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), err)
		return
	}
	switch {
	case body.FontSize < 10 || body.FontSize > 48:
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusBadRequest, CodeBadRequest,
			"font_size must be between 10 and 48.")
		return
	case body.LineSpacing < 1.0 || body.LineSpacing > 3.0:
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusBadRequest, CodeBadRequest,
			"line_spacing must be between 1.0 and 3.0.")
		return
	case body.Margin < 0 || body.Margin > 200:
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusBadRequest, CodeBadRequest,
			"margin must be between 0 and 200.")
		return
	case body.ReadingWidth < 320 || body.ReadingWidth > 2000:
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusBadRequest, CodeBadRequest,
			"reading_width must be between 320 and 2000.")
		return
	case !validReaderTheme(body.Theme):
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusBadRequest, CodeBadRequest,
			`theme must be one of "light", "dark", "sepia".`)
		return
	}

	if s.userSettings == nil {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusServiceUnavailable, CodeServiceUnavailable,
			"Settings are not available.")
		return
	}
	if err := s.userSettings.SetJSON(r.Context(), u.ID, readerPreferencesKey, body); err != nil {
		s.logger.Error("set reader preferences", "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Internal server error.")
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"preferences": body})
}
