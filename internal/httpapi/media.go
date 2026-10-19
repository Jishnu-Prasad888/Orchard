package httpapi

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/Jishnu-Prasad888/Orchard/internal/auth"
	"github.com/Jishnu-Prasad888/Orchard/internal/authz"
	"github.com/Jishnu-Prasad888/Orchard/internal/library"
	"github.com/Jishnu-Prasad888/Orchard/internal/librarydb"
	"github.com/Jishnu-Prasad888/Orchard/internal/media"
	"github.com/Jishnu-Prasad888/Orchard/internal/uploads"
)

// openMediaService opens the per-library DB and returns a media.Service and
// a cleanup func. Returns an HTTP error response when the library is offline
// or the DB cannot be opened.
func (s *Server) openMediaService(
	w http.ResponseWriter, r *http.Request, lib *library.Library,
) (*media.Service, func(), bool) {
	if lib.Status == library.StatusOffline {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusConflict,
			CodeConflict, "Library is offline.")
		return nil, nil, false
	}

	orchardDir := lib.Root + "/.orchard"
	db, err := librarydb.Open(orchardDir)
	if err != nil {
		s.logger.Error("open library db", "library_id", lib.ID, "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to open library database.")
		return nil, nil, false
	}
	store := media.NewFileStore(db, lib.ID)
	svc := media.NewService(store, lib.Root, media.WithMaxUploadBytes(s.maxUploadBytes))
	cleanup := func() { _ = db.Close() }
	return svc, cleanup, true
}

// handleListFiles — GET /api/v1/libraries/{id}/files
func (s *Server) handleListFiles(w http.ResponseWriter, r *http.Request, u *auth.User) {
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

	opts := parseListOptions(r)
	folder, ok := canonicalFolder(opts.FolderPath)
	if !ok {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusBadRequest,
			CodeBadRequest, "Invalid folder path.")
		return
	}
	opts.FolderPath = folder
	if !s.requireCap(w, r, u, folderKeyFromParent(lib.ID, opts.FolderPath), authz.CapRead) {
		return
	}
	page, err := svc.Store().List(r.Context(), opts)
	if err != nil {
		s.logger.Error("list files", "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to list files.")
		return
	}
	out := toFileResponses(page.Files)
	attachFileExtras(out, s.fileExtrasFor(r.Context(), lib, u.ID, page.Files))
	writeJSON(w, s.logger, http.StatusOK, map[string]any{
		"files":       out,
		"next_cursor": page.NextCursor,
		"total":       page.Total,
	})
}

// handleFileCounts — GET /api/v1/libraries/{id}/files/counts
func (s *Server) handleFileCounts(w http.ResponseWriter, r *http.Request, u *auth.User) {
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
	counts, err := svc.Store().CountsByType(r.Context())
	if err != nil {
		s.logger.Error("count files", "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to count files.")
		return
	}
	coll, err := svc.Store().CountCollections(r.Context(), u.ID)
	if err != nil {
		s.logger.Error("count collections", "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to count files.")
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{
		"counts":    counts,
		"tags":      coll.Tags,
		"favorites": coll.Favorites,
	})
}

// handleGetFile — GET /api/v1/libraries/{id}/files/{fileID}
func (s *Server) handleGetFile(w http.ResponseWriter, r *http.Request, u *auth.User) {
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

	f, err := svc.Store().GetByID(r.Context(), r.PathValue("fileID"))
	if err != nil {
		s.writeMediaError(w, r, err)
		return
	}
	if !s.requireCap(w, r, u, authz.FileKey(lib.ID, f.RelPath), authz.CapRead) {
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"file": toFileResponse(f)})
}

// handleDownloadFile — GET /api/v1/libraries/{id}/files/{fileID}/download
// Streams the file with Range support for video seeking.
func (s *Server) handleDownloadFile(w http.ResponseWriter, r *http.Request, u *auth.User) {
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

	f, err := svc.Store().GetByRelPath(r.Context(), r.URL.Query().Get("path"))
	if err != nil {
		// Fall back to file ID lookup.
		f, err = svc.Store().GetByID(r.Context(), r.PathValue("fileID"))
		if err != nil {
			s.writeMediaError(w, r, err)
			return
		}
	}
	if !s.requireCap(w, r, u, authz.FileKey(lib.ID, f.RelPath), authz.CapDownload) {
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
		fmt.Sprintf(`attachment; filename=%q`, f.Name))
	w.Header().Set("Accept-Ranges", "bytes")
	http.ServeContent(w, r, f.Name, f.ModTime, fh)
}

// handleUploadFile — POST /api/v1/libraries/{id}/files/upload
//
// The one-shot multipart upload, kept for small files and simple clients;
// the web UI uses the resumable /uploads protocol (uploads.go). The form is
// streamed part by part rather than parsed with ParseMultipartForm, which
// would hold 32 MiB in memory and spool the rest to /tmp — on a Raspberry Pi
// often a RAM disk. The file lands in the library's .orchard/uploads and is
// renamed into place, so it is written once.
func (s *Server) handleUploadFile(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, ok := s.onlineLibrary(w, r, u)
	if !ok {
		return
	}
	reqID := requestIDOrEmpty(r)
	limit := s.uploadLimit()
	// A small margin absorbs multipart part headers and form fields.
	r.Body = http.MaxBytesReader(w, r.Body, limit+(1<<20))
	body := newStallReader(r.Body, http.NewResponseController(w))
	r.Body = io.NopCloser(body)
	mr, err := r.MultipartReader()
	if err != nil {
		writeError(w, s.logger, reqID, http.StatusBadRequest, CodeBadRequest,
			"The upload wasn’t sent as a multipart form.")
		return
	}

	store := uploads.Open(lib.Root)
	var destPath, fileName, tmpPath string
	defer func() {
		if tmpPath != "" {
			_ = os.Remove(tmpPath) // no-op once committed
		}
	}()
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			s.writeUploadError(w, r, err)
			return
		}
		switch {
		case part.FormName() == "path":
			raw, err := io.ReadAll(io.LimitReader(part, 4096))
			if err != nil {
				s.writeUploadError(w, r, err)
				return
			}
			destPath = string(raw)
		case part.FormName() == "file" && tmpPath == "":
			fileName = part.FileName()
			tmp, err := store.CreateTemp()
			if err != nil {
				s.writeUploadError(w, r, err)
				return
			}
			tmpPath = tmp.Name()
			n, copyErr := io.Copy(tmp, io.LimitReader(part, limit+1))
			closeErr := tmp.Close()
			if copyErr == nil {
				copyErr = closeErr
			}
			if copyErr != nil {
				s.writeUploadError(w, r, copyErr)
				return
			}
			if n > limit {
				s.writeUploadError(w, r, media.ErrUploadTooLarge)
				return
			}
		}
		_ = part.Close()
	}
	body.done()
	if tmpPath == "" {
		writeError(w, s.logger, reqID, http.StatusBadRequest, CodeBadRequest,
			"Missing 'file' field in form.")
		return
	}
	if destPath == "" {
		destPath = fileName
	}
	if !s.requireCap(w, r, u, parentFolderKey(lib.ID, destPath), authz.CapCreate) {
		return
	}

	svc, cleanup, ok := s.openMediaService(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	created, err := svc.CommitUpload(r.Context(), destPath, tmpPath)
	if err != nil {
		s.writeUploadError(w, r, err, destPath)
		return
	}
	writeJSON(w, s.logger, http.StatusCreated, map[string]any{"file": toFileResponse(created)})
}

// handleRenameFile — POST /api/v1/libraries/{id}/files/{fileID}/rename
func (s *Server) handleRenameFile(w http.ResponseWriter, r *http.Request, u *auth.User) {
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

	var body struct {
		Path    string `json:"path"`
		NewName string `json:"new_name"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), err)
		return
	}
	if body.NewName == "" {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusBadRequest,
			CodeBadRequest, "new_name is required.")
		return
	}
	if !s.requireCap(w, r, u, authz.FileKey(lib.ID, body.Path), authz.CapEdit) {
		return
	}
	updated, err := svc.Rename(r.Context(), body.Path, body.NewName)
	if err != nil {
		s.writeMediaError(w, r, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"file": toFileResponse(updated)})
}

// handleMoveFile — POST /api/v1/libraries/{id}/files/{fileID}/move
func (s *Server) handleMoveFile(w http.ResponseWriter, r *http.Request, u *auth.User) {
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

	var body struct {
		Path    string `json:"path"`
		NewPath string `json:"new_path"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), err)
		return
	}
	if !s.requireCap(w, r, u, authz.FileKey(lib.ID, body.Path), authz.CapMove) {
		return
	}
	updated, err := svc.Move(r.Context(), body.Path, body.NewPath)
	if err != nil {
		s.writeMediaError(w, r, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"file": toFileResponse(updated)})
}

// handleCopyFile — POST /api/v1/libraries/{id}/files/{fileID}/copy
func (s *Server) handleCopyFile(w http.ResponseWriter, r *http.Request, u *auth.User) {
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

	var body struct {
		Path     string `json:"path"`
		DestPath string `json:"dest_path"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), err)
		return
	}
	if !s.requireCap(w, r, u, authz.FileKey(lib.ID, body.Path), authz.CapRead) {
		return
	}
	if !s.requireCap(w, r, u, parentFolderKey(lib.ID, body.DestPath), authz.CapCreate) {
		return
	}
	created, err := svc.Copy(r.Context(), body.Path, body.DestPath)
	if err != nil {
		s.writeMediaError(w, r, err)
		return
	}
	writeJSON(w, s.logger, http.StatusCreated, map[string]any{"file": toFileResponse(created)})
}

// handleDeleteFile — DELETE /api/v1/libraries/{id}/files/{fileID}
func (s *Server) handleDeleteFile(w http.ResponseWriter, r *http.Request, u *auth.User) {
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

	var body struct {
		Path string `json:"path"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), err)
		return
	}
	if !s.requireCap(w, r, u, authz.FileKey(lib.ID, body.Path), authz.CapDelete) {
		return
	}
	actorID := ""
	if u != nil {
		actorID = u.ID
	}
	if err := svc.Delete(r.Context(), body.Path, actorID); err != nil {
		s.writeMediaError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRestoreFile — POST /api/v1/libraries/{id}/files/{fileID}/restore
func (s *Server) handleRestoreFile(w http.ResponseWriter, r *http.Request, u *auth.User) {
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

	if !s.fileKeyFor(w, r, u, lib, r.PathValue("fileID"), authz.CapEdit) {
		return
	}
	restored, err := svc.Restore(r.Context(), r.PathValue("fileID"))
	if err != nil {
		s.writeMediaError(w, r, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"file": toFileResponse(restored)})
}

// handlePermanentDeleteFile — DELETE /api/v1/libraries/{id}/files/{fileID}/permanent
func (s *Server) handlePermanentDeleteFile(w http.ResponseWriter, r *http.Request, u *auth.User) {
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

	if !s.fileKeyFor(w, r, u, lib, r.PathValue("fileID"), authz.CapDelete) {
		return
	}
	if err := svc.PermanentDelete(r.Context(), r.PathValue("fileID")); err != nil {
		s.writeMediaError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleListFolders — GET /api/v1/libraries/{id}/folders
func (s *Server) handleListFolders(w http.ResponseWriter, r *http.Request, u *auth.User) {
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

	parent := r.URL.Query().Get("parent")
	folder, ok := canonicalFolder(parent)
	if !ok {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusBadRequest,
			CodeBadRequest, "Invalid folder path.")
		return
	}
	parent = folder
	if !s.requireCap(w, r, u, folderKeyFromParent(lib.ID, parent), authz.CapRead) {
		return
	}
	folders, err := svc.ListFolders(r.Context(), parent)
	if err != nil {
		s.logger.Error("list folders", "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to list folders.")
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"folders": toFolderResponses(folders)})
}

// handleCreateFolder — POST /api/v1/libraries/{id}/folders
func (s *Server) handleCreateFolder(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	var body struct {
		Path string `json:"path"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), err)
		return
	}
	if !s.requireCap(w, r, u, parentFolderKey(lib.ID, body.Path), authz.CapCreate) {
		return
	}
	svc, cleanup, ok := s.openMediaService(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	folder, err := svc.CreateFolder(body.Path)
	if err != nil {
		s.writeMediaError(w, r, err)
		return
	}
	writeJSON(w, s.logger, http.StatusCreated, map[string]any{"folder": toFolderResponse(folder)})
}

// handleListTrash — GET /api/v1/libraries/{id}/trash
func (s *Server) handleListTrash(w http.ResponseWriter, r *http.Request, u *auth.User) {
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
	files, err := svc.Store().ListTrash(r.Context())
	if err != nil {
		s.logger.Error("list trash", "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to list trash.")
		return
	}
	out := toFileResponses(files)
	attachFileExtras(out, s.fileExtrasFor(r.Context(), lib, u.ID, files))
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"files": out})
}

// --- error mapping ---

func (s *Server) writeMediaError(w http.ResponseWriter, r *http.Request, err error) {
	reqID := requestIDOrEmpty(r)
	switch {
	case errors.Is(err, media.ErrNotFound), errors.Is(err, media.ErrFolderNotFound):
		writeError(w, s.logger, reqID, http.StatusNotFound, CodeNotFound, "File not found.")
	case errors.Is(err, media.ErrAlreadyExists):
		writeError(w, s.logger, reqID, http.StatusConflict, CodeConflict,
			"A file already exists at the destination.")
	case errors.Is(err, media.ErrInTrash):
		writeError(w, s.logger, reqID, http.StatusConflict, CodeConflict,
			"File is already in trash.")
	case errors.Is(err, media.ErrNotInTrash):
		writeError(w, s.logger, reqID, http.StatusConflict, CodeConflict,
			"File is not in trash.")
	case errors.Is(err, media.ErrPathTraversal):
		writeError(w, s.logger, reqID, http.StatusBadRequest, CodeBadRequest,
			"Invalid file path.")
	case errors.Is(err, media.ErrUploadTooLarge):
		writeError(w, s.logger, reqID, http.StatusRequestEntityTooLarge,
			CodePayloadTooLarge, "Upload exceeds the maximum allowed size.")
	default:
		s.logger.Error("media operation", "error", err)
		writeError(w, s.logger, reqID, http.StatusInternalServerError,
			CodeInternal, "Internal server error.")
	}
}

// --- response types ---

type fileResponse struct {
	ID          string `json:"id"`
	LibraryID   string `json:"library_id"`
	RelPath     string `json:"rel_path"`
	Name        string `json:"name"`
	FolderPath  string `json:"folder_path"`
	SizeBytes   int64  `json:"size_bytes"`
	ModTime     string `json:"mod_time"`
	MediaType   string `json:"media_type"`
	MIMEType    string `json:"mime_type"`
	Status      string `json:"status"`
	ContentHash string `json:"content_hash,omitempty"`
	// CoverURL is set only for a document file known to have an extracted
	// or user-uploaded cover (see fileExtrasFor) — a grid-listing handler
	// opts in by calling it, since most single-file responses have no use
	// for it.
	CoverURL string `json:"cover_url,omitempty"`
	// ProgressPercent is how far the acting user has read this book
	// (0-100), set the same way; absent when they have not started it.
	ProgressPercent float64 `json:"progress_percent,omitempty"`
}

type folderResponse struct {
	ID        string `json:"id"`
	LibraryID string `json:"library_id"`
	RelPath   string `json:"rel_path"`
	Name      string `json:"name"`
	FileCount int    `json:"file_count"`
}

func toFileResponse(f *media.File) fileResponse {
	return fileResponse{
		ID:          f.ID,
		LibraryID:   f.LibraryID,
		RelPath:     f.RelPath,
		Name:        f.Name,
		FolderPath:  f.FolderPath,
		SizeBytes:   f.SizeBytes,
		ModTime:     f.ModTime.UTC().Format(time.RFC3339),
		MediaType:   string(f.MediaType),
		MIMEType:    f.MIMEType,
		Status:      string(f.Status),
		ContentHash: f.ContentHash,
	}
}

func toFileResponses(files []*media.File) []fileResponse {
	out := make([]fileResponse, 0, len(files))
	for _, f := range files {
		out = append(out, toFileResponse(f))
	}
	return out
}

func toFolderResponse(f *media.Folder) folderResponse {
	return folderResponse{
		ID:        f.ID,
		LibraryID: f.LibraryID,
		RelPath:   f.RelPath,
		Name:      f.Name,
		FileCount: f.FileCount,
	}
}

func toFolderResponses(folders []*media.Folder) []folderResponse {
	out := make([]folderResponse, 0, len(folders))
	for _, f := range folders {
		out = append(out, toFolderResponse(f))
	}
	return out
}

// --- query param parsing ---

func parseListOptions(r *http.Request) media.ListOptions {
	q := r.URL.Query()
	opts := media.ListOptions{
		FolderPath: q.Get("folder"),
		Recursive:  q.Get("recursive") == "true",
		Type:       media.MediaType(q.Get("type")),
		Status:     media.FileStatus(q.Get("status")),
		Sort:       media.SortField(q.Get("sort")),
		Order:      media.SortOrder(q.Get("order")),
		Cursor:     q.Get("cursor"),
	}
	if lim := q.Get("limit"); lim != "" {
		if n, err := strconv.Atoi(lim); err == nil {
			opts.Limit = n
		}
	}
	return opts
}
