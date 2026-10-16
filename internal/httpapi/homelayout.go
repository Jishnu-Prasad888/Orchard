package httpapi

import (
	"net/http"

	"github.com/Jishnu-Prasad888/Orchard/internal/auth"
)

// homeLayoutKey is the user_settings key Home's section layout is stored
// under, as one JSON blob (order matters, so the whole list is always read
// and written together) — the same convention readerPreferencesKey follows.
const homeLayoutKey = "home.layout"

// HomeLayoutEntry is one Home section's position and visibility.
type HomeLayoutEntry struct {
	ID      string `json:"id"`
	Visible bool   `json:"visible"`
}

// homeSectionIDs is every section Home knows how to render, in their
// default order — also what a user with no saved layout sees.
var homeSectionIDs = []string{
	"continue-reading",
	"recently-opened",
	"recently-added",
	"favorites",
	"pinned-collections",
	"recent-quotes",
}

func defaultHomeLayout() []HomeLayoutEntry {
	out := make([]HomeLayoutEntry, len(homeSectionIDs))
	for i, id := range homeSectionIDs {
		out[i] = HomeLayoutEntry{ID: id, Visible: true}
	}
	return out
}

// sanitizeHomeLayout drops unknown/duplicate section IDs and appends any
// known section missing from entries, as visible, at the end — so a
// section added to Orchard after a user last customized their layout still
// appears, and a stale or hand-edited blob can never wedge a section out of
// existence.
func sanitizeHomeLayout(entries []HomeLayoutEntry) []HomeLayoutEntry {
	known := make(map[string]bool, len(homeSectionIDs))
	for _, id := range homeSectionIDs {
		known[id] = true
	}
	seen := make(map[string]bool, len(entries))
	out := make([]HomeLayoutEntry, 0, len(entries))
	for _, e := range entries {
		if !known[e.ID] || seen[e.ID] {
			continue
		}
		seen[e.ID] = true
		out = append(out, e)
	}
	for _, id := range homeSectionIDs {
		if !seen[id] {
			out = append(out, HomeLayoutEntry{ID: id, Visible: true})
		}
	}
	return out
}

// handleGetHomeLayout — GET /api/v1/settings/home-layout[?library=<id>]
//
// Like reader preferences: the library's copy wins, the account's stands in.
func (s *Server) handleGetHomeLayout(w http.ResponseWriter, r *http.Request, u *auth.User) {
	entries := defaultHomeLayout()
	if lib := s.memberSettingsLibrary(r, u); lib != nil &&
		s.getMemberSetting(r.Context(), lib, u.ID, homeLayoutKey, &entries) {
		writeJSON(w, s.logger, http.StatusOK, map[string]any{"sections": sanitizeHomeLayout(entries)})
		return
	}
	if s.userSettings != nil {
		if _, err := s.userSettings.GetJSON(r.Context(), u.ID, homeLayoutKey, &entries); err != nil {
			s.logger.Error("get home layout", "error", err)
			writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
				CodeInternal, "Internal server error.")
			return
		}
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"sections": sanitizeHomeLayout(entries)})
}

// handleSetHomeLayout — PUT /api/v1/settings/home-layout[?library=<id>]
func (s *Server) handleSetHomeLayout(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var body struct {
		Sections []HomeLayoutEntry `json:"sections"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), err)
		return
	}
	clean := sanitizeHomeLayout(body.Sections)

	if s.userSettings == nil {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusServiceUnavailable, CodeServiceUnavailable,
			"Settings are not available.")
		return
	}
	if err := s.userSettings.SetJSON(r.Context(), u.ID, homeLayoutKey, clean); err != nil {
		s.logger.Error("set home layout", "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Internal server error.")
		return
	}
	if lib := s.memberSettingsLibrary(r, u); lib != nil {
		if err := s.setMemberSetting(r.Context(), lib, u.ID, homeLayoutKey, clean); err != nil {
			s.logger.Error("set home layout in library", "library_id", lib.ID, "error", err)
			writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
				CodeInternal, "Internal server error.")
			return
		}
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"sections": clean})
}
