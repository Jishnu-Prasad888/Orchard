package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/Jishnu-Prasad888/Cairn/internal/authz"
	"github.com/Jishnu-Prasad888/Cairn/internal/library"
	"github.com/Jishnu-Prasad888/Cairn/internal/media"
)

// sharePasswordHeader carries an optional share password on public requests.
const sharePasswordHeader = "X-Cairn-Share-Password"

// shareAuthenticatedFunc is a handler that has already been granted access to
// a share.
type shareAccess struct {
	share   *authz.Share
	library *library.Library
	libID   string
}

// resolveShare authenticates the share token (and optional password) and
// resolves the owning library. Denied or malformed shares receive
// UNAUTHORIZED without revealing which check failed.
func (s *Server) resolveShare(w http.ResponseWriter, r *http.Request) (*shareAccess, bool) {
	sh, err := s.authz.AuthenticateShare(r.Context(), r.PathValue("token"), r.Header.Get(sharePasswordHeader))
	if err != nil {
		s.writeAuthzError(w, r, err)
		return nil, false
	}
	libID := shareLibraryID(sh.ResourceKey)
	lib, err := s.libraries.Get(r.Context(), libID)
	if err != nil {
		s.logger.Error("share library lookup", "library_id", libID, "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusNotFound, CodeNotFound,
			"Share not found.")
		return nil, false
	}
	return &shareAccess{share: sh, library: lib, libID: libID}, true
}

// shareCan verifies the share grants all given capabilities on the resource
// key. Share capabilities flow through the same grant evaluation as user
// permissions: a share never bypasses authorization, it only adds a grant
// rooted at its resource key, inherited by descendants.
func (a *shareAccess) shareCan(key string, caps ...authz.Capability) bool {
	return a.share.Capabilities.All(caps...) && coversKey(a.share.ResourceKey, key)
}

// shareLibraryID extracts the library id from a resource key prefix.
func shareLibraryID(key string) string {
	if i := strings.Index(key, "/"); i > 0 {
		return key[:i]
	}
	return key
}

// coversKey reports whether ancestor shares or equals key at a "/" boundary.
func coversKey(ancestor, key string) bool {
	if ancestor == key {
		return true
	}
	return strings.HasPrefix(key, ancestor+"/")
}

// handlePublicShareInfo — GET /api/v1/shares/{token}
// Public share metadata (no capability data leaks; only existence + scope).
func (s *Server) handlePublicShareInfo(w http.ResponseWriter, r *http.Request) {
	acc, ok := s.resolveShare(w, r)
	if !ok {
		return
	}
	// Even without read on the scope, the share info shows what the share
	// covers and the owning library name.
	writeJSON(w, s.logger, http.StatusOK, map[string]any{
		"share": map[string]any{
			"resource_key": acc.share.ResourceKey,
			"library":      acc.library.Name,
			"has_password": acc.share.PasswordHash.Valid,
			"expires_at":   shareExpiresString(acc.share),
		},
	})
}

func shareExpiresString(sh *authz.Share) any {
	if sh.ExpiresAt == nil {
		return nil
	}
	return sh.ExpiresAt.UTC().Format(time.RFC3339Nano)
}

// handlePublicShareListFiles — GET /api/v1/shares/{token}/files
func (s *Server) handlePublicShareListFiles(w http.ResponseWriter, r *http.Request) {
	acc, ok := s.resolveShare(w, r)
	if !ok {
		return
	}
	svc, cleanup, ok := s.openMediaService(w, r, acc.library)
	if !ok {
		return
	}
	defer cleanup()

	folder := r.URL.Query().Get("folder")
	if !acc.shareCan(folderKeyFromParent(acc.libID, folder), authz.CapRead) {
		s.writeForbidden(w, r)
		return
	}

	opts := parseListOptions(r)
	opts.FolderPath = folder
	page, err := svc.Store().List(r.Context(), opts)
	if err != nil {
		s.logger.Error("share list files", "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to list files.")
		return
	}
	// Filter out files that fall outside the share's granted scope.
	files := make([]*media.File, 0, len(page.Files))
	for _, f := range page.Files {
		if acc.shareCan(authz.FileKey(acc.libID, f.RelPath), authz.CapRead) {
			files = append(files, f)
		}
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{
		"files":       toFileResponses(files),
		"next_cursor": page.NextCursor,
		"total":       page.Total,
	})
}

// handlePublicShareGetFile — GET /api/v1/shares/{token}/files/{fileID}
func (s *Server) handlePublicShareGetFile(w http.ResponseWriter, r *http.Request) {
	acc, ok := s.resolveShare(w, r)
	if !ok {
		return
	}
	svc, cleanup, ok := s.openMediaService(w, r, acc.library)
	if !ok {
		return
	}
	defer cleanup()

	f, err := svc.Store().GetByID(r.Context(), r.PathValue("fileID"))
	if err != nil {
		s.writeMediaError(w, r, err)
		return
	}
	if !acc.shareCan(authz.FileKey(acc.libID, f.RelPath), authz.CapRead) {
		s.writeForbidden(w, r)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"file": toFileResponse(f)})
}

// handlePublicShareDownload — GET /api/v1/shares/{token}/files/{fileID}/download
func (s *Server) handlePublicShareDownload(w http.ResponseWriter, r *http.Request) {
	acc, ok := s.resolveShare(w, r)
	if !ok {
		return
	}
	svc, cleanup, ok := s.openMediaService(w, r, acc.library)
	if !ok {
		return
	}
	defer cleanup()

	f, err := svc.Store().GetByID(r.Context(), r.PathValue("fileID"))
	if err != nil {
		s.writeMediaError(w, r, err)
		return
	}
	if !acc.shareCan(authz.FileKey(acc.libID, f.RelPath), authz.CapRead, authz.CapDownload) {
		s.writeForbidden(w, r)
		return
	}

	fh, _, err := svc.OpenFile(r.Context(), f.RelPath)
	if err != nil {
		s.writeMediaError(w, r, err)
		return
	}
	defer func() { _ = fh.Close() }()

	w.Header().Set("Content-Type", f.MIMEType)
	w.Header().Set("Content-Disposition",
		`attachment; filename="`+f.Name+`"`)
	http.ServeContent(w, r, f.Name, f.ModTime, fh)
}

// handlePublicShareAuthenticate — POST /api/v1/shares/{token}/authenticate
// Validates the share token (and password, when set) and returns whether
// access is granted. Public share clients send the password header on
// subsequent requests; this endpoint exists so the web UI can confirm a
// password before navigating.
func (s *Server) handlePublicShareAuthenticate(w http.ResponseWriter, r *http.Request) {
	if !s.limitPublicAuth(w, r, "") {
		return
	}
	if _, ok := s.resolveShare(w, r); !ok {
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"authenticated": true})
}
