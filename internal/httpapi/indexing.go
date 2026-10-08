package httpapi

import (
	"errors"
	"net/http"

	"github.com/Jishnu-Prasad888/Cairn/internal/auth"
	"github.com/Jishnu-Prasad888/Cairn/internal/indexer"
	"github.com/Jishnu-Prasad888/Cairn/internal/library"
)

// handleTriggerIndex enqueues a new scan job for the given library.
// POST /api/v1/libraries/{id}/index
func (s *Server) handleTriggerIndex(w http.ResponseWriter, r *http.Request, u *auth.User) {
	if s.indexer == nil {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusServiceUnavailable,
			CodeInternal, "Indexer not available.")
		return
	}
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if lib.Status == library.StatusOffline {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusConflict,
			CodeConflict, "Library is offline; cannot start index scan.")
		return
	}

	jobID, err := s.indexer.TriggerScan(r.Context(), lib.ID, lib.Root)
	if err != nil {
		s.logger.Error("trigger index", "library_id", lib.ID, "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to enqueue index scan.")
		return
	}
	writeJSON(w, s.logger, http.StatusAccepted, map[string]any{
		"job_id":     jobID,
		"library_id": lib.ID,
	})
}

// handleIndexStatus returns the current index status for the given library.
// GET /api/v1/libraries/{id}/index/status
func (s *Server) handleIndexStatus(w http.ResponseWriter, r *http.Request, u *auth.User) {
	if s.indexer == nil {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusServiceUnavailable,
			CodeInternal, "Indexer not available.")
		return
	}
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}

	status, err := s.indexer.Status(r.Context(), lib.ID, lib.Root)
	if err != nil {
		// For offline libraries the library DB may not exist yet; treat as empty.
		if lib.Status == library.StatusOffline || isLibraryDBMissing(err) {
			writeJSON(w, s.logger, http.StatusOK, map[string]any{
				"status": &indexer.IndexStatus{LibraryID: lib.ID},
			})
			return
		}
		s.logger.Error("index status", "library_id", lib.ID, "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to retrieve index status.")
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"status": status})
}

// isLibraryDBMissing returns true when the error indicates the per-library
// database does not yet exist (library was registered but never scanned).
func isLibraryDBMissing(err error) bool {
	if err == nil {
		return false
	}
	// The library database open path calls os.Stat; "no such file" means the
	// .cairn directory or library.db does not exist yet.
	return errors.Is(err, library.ErrNotFound) ||
		containsAny(err.Error(), "no such file", "does not exist")
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(s) >= len(sub) {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
		}
	}
	return false
}
