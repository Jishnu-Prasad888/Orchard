package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/Jishnu-Prasad888/Cairn/internal/auth"
	"github.com/Jishnu-Prasad888/Cairn/internal/authz"
	"github.com/Jishnu-Prasad888/Cairn/internal/library"
	"github.com/Jishnu-Prasad888/Cairn/internal/media"
)

// handleMLStatus — GET /api/v1/libraries/{id}/ml
// Returns the ML similarity capability state for the library.
func (s *Server) handleMLStatus(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if lib.Status == library.StatusOffline {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusConflict,
			CodeConflict, "Library is offline.")
		return
	}
	st, err := s.ml.Status(r.Context(), lib.ID, lib.Root)
	if err != nil {
		s.logger.Error("ml status", "library_id", lib.ID, "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to retrieve ML status.")
		return
	}
	writeJSON(w, s.logger, http.StatusOK, st)
}

// handleMLPass — POST /api/v1/libraries/{id}/ml/similarity/pass
// Enqueues a similarity pass over the library. The pass runs in the
// background with bounded concurrency; the response reports the work begun.
func (s *Server) handleMLPass(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if lib.Status == library.StatusOffline {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusConflict,
			CodeConflict, "Library is offline; cannot run a similarity pass.")
		return
	}
	if !s.ml.Enabled() {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusServiceUnavailable,
			CodeServiceUnavailable, "Local ML is disabled.")
		return
	}

	// Run the pass detached from the request context so a slow pass is not
	// cancelled when the handler returns; bound it with a generous timeout so
	// a hung decode cannot leak forever.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		if _, err := s.ml.Pass(ctx, lib.ID, lib.Root); err != nil {
			s.logger.Error("ml similarity pass", "library_id", lib.ID, "error", err)
		}
	}()

	writeJSON(w, s.logger, http.StatusAccepted, map[string]any{
		"library_id": lib.ID,
		"status":     "similarity pass started",
	})
}

// handleMLPurge — POST /api/v1/libraries/{id}/ml/purge
// Deletes all derived similarity signatures for the library. Originals are
// untouched; the pass can regenerate them.
func (s *Server) handleMLPurge(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.ml.Enabled() {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusServiceUnavailable,
			CodeServiceUnavailable, "Local ML is disabled.")
		return
	}
	n, err := s.ml.Purge(r.Context(), lib.ID, lib.Root)
	if err != nil {
		s.logger.Error("ml purge", "library_id", lib.ID, "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to purge ML signatures.")
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{
		"library_id": lib.ID,
		"purged":     n,
	})
}

// handleSimilarFiles — GET /api/v1/libraries/{id}/files/{fileID}/similar
// Returns files in the library whose perceptual signature is near the given
// file's. Follows the same per-file Read capability as other content routes.
func (s *Server) handleSimilarFiles(w http.ResponseWriter, r *http.Request, u *auth.User) {
	fileID := r.PathValue("fileID")
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.ml.Enabled() {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusServiceUnavailable,
			CodeServiceUnavailable, "Local ML is disabled.")
		return
	}

	svc, cleanup, ok := s.openMediaService(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	f, err := svc.Store().GetByID(r.Context(), fileID)
	if err != nil {
		s.writeMediaError(w, r, err)
		return
	}
	if !s.requireCap(w, r, u, authz.FileKey(lib.ID, f.RelPath), authz.CapRead) {
		return
	}

	hits, err := s.ml.Similar(r.Context(), lib.ID, lib.Root, fileID, 50)
	if err != nil {
		s.logger.Error("ml similar", "library_id", lib.ID, "file_id", fileID, "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to compute similar files.")
		return
	}

	out := make([]map[string]any, 0, len(hits))
	for _, h := range hits {
		rel := ""
		if hf, err := svc.Store().GetByID(r.Context(), h.FileID); err == nil {
			rel = hf.RelPath
		}
		out = append(out, map[string]any{
			"file_id":    h.FileID,
			"file_path":  rel,
			"distance":   h.Distance,
			"similarity": h.Similarity,
		})
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"similar": out})
}

// handleSimilarityGroups — GET /api/v1/libraries/{id}/ml/similarity/groups
// Returns every group of two or more present photos whose perceptual
// signatures are within the configured distance of one another, so the
// library's visual near-duplicates can be reviewed all at once instead of one
// file at a time.
func (s *Server) handleSimilarityGroups(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.ml.Enabled() {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusServiceUnavailable,
			CodeServiceUnavailable, "Local ML is disabled.")
		return
	}

	svc, cleanup, ok := s.openMediaService(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	// Grouping spans the whole library, so it follows the library-wide read
	// capability rather than any single file's or folder's.
	if !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapRead) {
		return
	}

	clusters, err := s.ml.Groups(r.Context(), lib.ID, lib.Root)
	if err != nil {
		s.logger.Error("ml similarity groups", "library_id", lib.ID, "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to group similar files.")
		return
	}

	groups := make([]similarityGroupResponse, 0, len(clusters))
	for _, cluster := range clusters {
		files := make([]*media.File, 0, len(cluster.FileIDs))
		for _, id := range cluster.FileIDs {
			f, err := svc.Store().GetByID(r.Context(), id)
			if err != nil {
				continue // removed since the signature was written
			}
			files = append(files, f)
		}
		if len(files) < 2 {
			continue
		}
		groups = append(groups, similarityGroupResponse{Files: toFileResponses(files)})
	}

	writeJSON(w, s.logger, http.StatusOK, map[string]any{
		"groups": groups,
		"total":  len(groups),
	})
}
