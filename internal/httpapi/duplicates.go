package httpapi

import (
	"net/http"

	"github.com/Jishnu-Prasad888/Orchard/internal/auth"
	"github.com/Jishnu-Prasad888/Orchard/internal/authz"
)

// duplicateGroupResponse is one set of files sharing a content hash.
type duplicateGroupResponse struct {
	ContentHash string         `json:"content_hash"`
	Files       []fileResponse `json:"files"`
}

// handleListDuplicates — GET /api/v1/libraries/{id}/files/duplicates
func (s *Server) handleListDuplicates(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	svc, cleanup, ok := s.openMediaService(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()
	if !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapRead) {
		return
	}

	groups, err := svc.Store().ListDuplicateGroups(r.Context())
	if err != nil {
		s.logger.Error("list duplicates", "library_id", lib.ID, "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to list duplicate files.")
		return
	}
	resp := make([]duplicateGroupResponse, 0, len(groups))
	for _, g := range groups {
		resp = append(resp, duplicateGroupResponse{
			ContentHash: g.ContentHash,
			Files:       toFileResponses(g.Files),
		})
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"groups": resp})
}
