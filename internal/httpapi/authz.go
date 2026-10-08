package httpapi

import (
	"net/http"
	"path/filepath"

	"github.com/Jishnu-Prasad888/Cairn/internal/auth"
	"github.com/Jishnu-Prasad888/Cairn/internal/authz"
	"github.com/Jishnu-Prasad888/Cairn/internal/library"
)

// principalFrom converts an authenticated account into the authorization
// principal. Admin is passed through as the convenience layer; every other
// decision flows through grants only.
func principalFrom(u *auth.User) authz.Principal {
	return authz.Principal{UserID: u.ID, Admin: u.Role == auth.RoleAdmin}
}

// requireCap is the single gate for resource-based authorization in the HTTP
// layer. Handlers resolve the resource they operate on, compute its resource
// key, and ask this helper whether the principal may perform the capabilities.
//
// It centralizes the decision (and the 403 envelope) so no handler reaches for
// ad-hoc role checks. When the authorization service is not wired (deps.Authz
// nil), the pre-Phase-9 admin gate is preserved so the API never silently opens
// up.
//
// Returns true when the request may proceed.
func (s *Server) requireCap(w http.ResponseWriter, r *http.Request, u *auth.User, key string, caps ...authz.Capability) bool {
	if s.authz == nil {
		if u.Role == auth.RoleAdmin {
			return true
		}
		s.writeForbidden(w, r)
		return false
	}
	ok, err := s.authz.Can(r.Context(), principalFrom(u), key, caps...)
	if err != nil {
		s.logger.Error("authorization check", "key", key, "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Internal server error.")
		return false
	}
	if !ok {
		s.writeForbidden(w, r)
		return false
	}
	return true
}

// writeForbidden emits the standard 403 envelope without leaking which
// capability or key was denied.
func (s *Server) writeForbidden(w http.ResponseWriter, r *http.Request) {
	writeError(w, s.logger, requestIDOrEmpty(r), http.StatusForbidden, CodeForbidden,
		"You do not have permission to perform this action.")
}

// fileKeyFor resolves a file's resource key by id and verifies the principal
// has all capabilities on it. Handlers that act on a file they have not
// already loaded (tags, favorites) use this instead of an ad-hoc lookup.
func (s *Server) fileKeyFor(
	w http.ResponseWriter, r *http.Request, u *auth.User, lib *library.Library, fileID string, caps ...authz.Capability,
) bool {
	svc, cleanup, ok := s.openMediaService(w, r, lib)
	if !ok {
		return false
	}
	defer cleanup()
	f, err := svc.Store().GetByID(r.Context(), fileID)
	if err != nil {
		s.writeMediaError(w, r, err)
		return false
	}
	return s.requireCap(w, r, u, authz.FileKey(lib.ID, f.RelPath), caps...)
}

// folderKeyFromParent maps a folder path (or the library root) to a resource
// key. A "." or empty path means the library root.
func folderKeyFromParent(libID, parent string) string {
	if parent == "" || parent == "." {
		return authz.LibraryKey(libID)
	}
	return authz.FolderKey(libID, parent)
}

// parentFolderKey maps a file's destination path to its containing folder key.
func parentFolderKey(libID, relPath string) string {
	parent := filepath.ToSlash(filepath.Dir(relPath))
	if parent == "." {
		parent = ""
	}
	return folderKeyFromParent(libID, parent)
}
