package httpapi

import (
	"errors"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Jishnu-Prasad888/Orchard/internal/authz"
	"github.com/Jishnu-Prasad888/Orchard/internal/library"
	"github.com/Jishnu-Prasad888/Orchard/internal/media"
)

// sharePasswordHeader carries an optional share password on public requests.
const sharePasswordHeader = "X-Orchard-Share-Password"

// shareAccess is a share that has been authenticated, with its library and
// what it exposes.
type shareAccess struct {
	share   *authz.Share
	library *library.Library
	libID   string
	target  shareTarget
}

// resolveShare authenticates the share token (and optional password) and
// resolves the owning library. Denied or malformed shares receive
// UNAUTHORIZED without revealing which check failed.
//
// Every public route takes the password, so every one counts wrong guesses:
// after a run of failures this client is locked out of this link for a
// while. Checking the lockout costs no token, so browsing a share is never
// throttled.
func (s *Server) resolveShare(w http.ResponseWriter, r *http.Request) (*shareAccess, bool) {
	ip, limitKey := clientIP(r), "share:"+r.PathValue("token")
	if allowed, retryAfter := s.ratelimit.allow(ip, limitKey, false); !allowed {
		w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds()+1)))
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusTooManyRequests,
			CodeRateLimited, "Too many attempts. Try again shortly.")
		return nil, false
	}
	sh, err := s.authenticateShareRequest(w, r)
	if err != nil {
		if errors.Is(err, authz.ErrUnauthorized) {
			s.ratelimit.recordFailure(ip, limitKey)
		}
		s.writeAuthzError(w, r, err)
		return nil, false
	}
	s.ratelimit.recordSuccess(ip, limitKey)
	libID := shareLibraryID(sh.ResourceKey)
	lib, err := s.libraries.Get(r.Context(), libID)
	if err != nil {
		s.logger.Error("share library lookup", "library_id", libID, "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusNotFound, CodeNotFound,
			"Share not found.")
		return nil, false
	}
	return &shareAccess{
		share:   sh,
		library: lib,
		libID:   libID,
		target:  parseShareTarget(libID, sh.ResourceKey),
	}, true
}

// authenticateShareRequest checks the link's password header, or, with no
// header, the link's unlock cookie (see sharesessions.go). A right password
// sets that cookie, so the page's images, videos and readers can load files
// without sending the password themselves.
func (s *Server) authenticateShareRequest(w http.ResponseWriter, r *http.Request) (*authz.Share, error) {
	token := r.PathValue("token")
	password := r.Header.Get(sharePasswordHeader)
	cookie := ""
	if c, err := r.Cookie(shareSessionCookie); err == nil {
		cookie = c.Value
	}
	if password == "" && cookie != "" {
		if sh, err := s.authz.ShareForUnlocked(r.Context(), token); err == nil &&
			(!sh.PasswordHash.Valid || s.shareSessions.unlocked(cookie, sh.ID)) {
			return sh, nil
		}
	}
	sh, err := s.authz.AuthenticateShare(r.Context(), token, password)
	if err != nil {
		return nil, err
	}
	if sh.PasswordHash.Valid && !s.shareSessions.unlocked(cookie, sh.ID) {
		if id, err := s.shareSessions.issue(sh.ID); err == nil {
			s.setShareCookie(w, r, token, id)
		}
	}
	return sh, nil
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
// What the share is (its kind and name), never its internal key or ids.
// For a folder share, folder is where browsing starts.
func (s *Server) handlePublicShareInfo(w http.ResponseWriter, r *http.Request) {
	acc, ok := s.resolveShare(w, r)
	if !ok {
		return
	}
	info := map[string]any{
		"kind":         acc.target.Kind,
		"name":         s.shareTargetName(r.Context(), acc.library, acc.share, acc.target),
		"library":      acc.library.Name,
		"has_password": acc.share.PasswordHash.Valid,
		"expires_at":   shareExpiresString(acc.share),
		"can_download": acc.share.Capabilities.All(authz.CapRead, authz.CapDownload),
	}
	if acc.target.Kind == shareKindFolder {
		info["folder"] = acc.target.Path
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"share": info})
}

func shareExpiresString(sh *authz.Share) any {
	if sh.ExpiresAt == nil {
		return nil
	}
	return sh.ExpiresAt.UTC().Format(time.RFC3339Nano)
}

// handlePublicShareListFiles — GET /api/v1/shares/{token}/files[?folder=]
//
// A library or folder share lists one folder at a time — the share's own
// folder unless ?folder= names one inside it — with its sub-folders, so a
// visitor can browse down but never above the share. A file share lists that
// one file; a tag or collection share lists the files in it now.
func (s *Server) handlePublicShareListFiles(w http.ResponseWriter, r *http.Request) {
	acc, ok := s.resolveShare(w, r)
	if !ok {
		return
	}
	if !acc.share.Capabilities.All(authz.CapRead) {
		s.writeForbidden(w, r)
		return
	}
	svc, cleanup, ok := s.openMediaService(w, r, acc.library)
	if !ok {
		return
	}
	defer cleanup()

	switch acc.target.Kind {
	case shareKindFile:
		f, err := svc.Store().GetByRelPath(r.Context(), acc.target.Path)
		files := []*media.File{}
		if err == nil && f.Status == media.FileStatusPresent {
			files = append(files, f)
		}
		writeJSON(w, s.logger, http.StatusOK, map[string]any{
			"files": toFileResponses(files), "folders": []folderResponse{}, "total": len(files),
		})
		return
	case shareKindTag, shareKindCollection:
		files, err := s.shareMemberFiles(r.Context(), acc)
		if err != nil {
			s.logger.Error("share list members", "error", err)
			writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
				CodeInternal, "Failed to list files.")
			return
		}
		writeJSON(w, s.logger, http.StatusOK, map[string]any{
			"files": toFileResponses(files), "folders": []folderResponse{}, "total": len(files),
		})
		return
	}

	folder, ok := canonicalFolder(r.URL.Query().Get("folder"))
	if !ok {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusBadRequest,
			CodeBadRequest, "Invalid folder path.")
		return
	}
	if r.URL.Query().Get("folder") == "" {
		folder = acc.target.Path
	}
	if !acc.shareCan(folderKeyFromParent(acc.libID, folder), authz.CapRead) {
		s.writeForbidden(w, r)
		return
	}

	opts := parseListOptions(r)
	opts.FolderPath = folder
	opts.Recursive = false
	page, err := svc.Store().List(r.Context(), opts)
	if err != nil {
		s.logger.Error("share list files", "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to list files.")
		return
	}
	// The folder is inside the share, so everything in it is too; checked per
	// file anyway, so a mistake above can never widen what a link shows.
	files := make([]*media.File, 0, len(page.Files))
	for _, f := range page.Files {
		if acc.shareCan(authz.FileKey(acc.libID, f.RelPath), authz.CapRead) {
			files = append(files, f)
		}
	}
	// Sub-folders only on the first page; later pages are more files.
	folders := []*media.Folder{}
	if opts.Cursor == "" {
		if folders, err = svc.ListFolders(r.Context(), folder); err != nil {
			s.logger.Error("share list folders", "error", err)
			folders = []*media.Folder{}
		}
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{
		"files":       toFileResponses(files),
		"folders":     toFolderResponses(folders),
		"folder":      folder,
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
	if !s.shareAllows(w, r, acc, f, authz.CapRead) {
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
	if !s.shareAllows(w, r, acc, f, authz.CapRead, authz.CapDownload) {
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
		mime.FormatMediaType("attachment", map[string]string{"filename": f.Name}))
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

// shareAllows writes 403 (or 500) and returns false unless the share exposes
// f with every given capability.
func (s *Server) shareAllows(
	w http.ResponseWriter, r *http.Request, acc *shareAccess, f *media.File, caps ...authz.Capability,
) bool {
	ok, err := s.shareCovers(r.Context(), acc, f, caps...)
	if err != nil {
		s.logger.Error("share access check", "error", err)
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

// handlePublicShareContent — GET /api/v1/shares/{token}/files/{fileID}/content
//
// The bytes of a shared file for showing it in the page: an image or video
// element, or a reader. It needs only view, so a link that does not allow
// downloads can still be looked at. It is still sent as an attachment, like
// every file Orchard serves: an element or a reader displays it all the same,
// but opening it as a page downloads it rather than rendering it, so a shared
// HTML or SVG file can never run in Orchard's origin.
func (s *Server) handlePublicShareContent(w http.ResponseWriter, r *http.Request) {
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
	if !s.shareAllows(w, r, acc, f, authz.CapRead) {
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
		mime.FormatMediaType("attachment", map[string]string{"filename": f.Name}))
	w.Header().Set("Accept-Ranges", "bytes")
	// Bytes a visitor may look at, not keep around: no shared caches.
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, f.Name, f.ModTime, fh)
}
