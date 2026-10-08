package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/Jishnu-Prasad888/Cairn/internal/auth"
	"github.com/Jishnu-Prasad888/Cairn/internal/authz"
	"github.com/Jishnu-Prasad888/Cairn/internal/library"
	"github.com/Jishnu-Prasad888/Cairn/internal/librarydb"
	"github.com/Jishnu-Prasad888/Cairn/internal/media"
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

	cairnDir := lib.Root + "/.cairn"
	db, err := librarydb.Open(cairnDir)
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
	writeJSON(w, s.logger, http.StatusOK, map[string]any{
		"files":       toFileResponses(page.Files),
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
	coll, err := svc.Store().CountCollections(r.Context())
	if err != nil {
		s.logger.Error("count collections", "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to count files.")
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{
		"counts":    counts,
		"albums":    coll.Albums,
		"tags":      coll.Tags,
		"favorites": coll.Favorites,
	})
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

	// Duplicate detection is a library-wide read, so it requires read access
	// to the library scope rather than to any single folder.
	if !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapRead) {
		return
	}

	opts := media.DuplicateOptions{Cursor: r.URL.Query().Get("cursor")}
	if lim := r.URL.Query().Get("limit"); lim != "" {
		if n, err := strconv.Atoi(lim); err == nil {
			opts.Limit = n
		}
	}
	opts.Defaults()

	page, err := svc.Store().ListDuplicates(r.Context(), opts)
	if err != nil {
		s.logger.Error("list duplicates", "library_id", lib.ID, "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to list duplicate files.")
		return
	}

	groups := make([]duplicateGroupResponse, 0, len(page.Groups))
	for _, g := range page.Groups {
		groups = append(groups, duplicateGroupResponse{
			ContentHash: g.ContentHash,
			SizeBytes:   g.SizeBytes,
			Files:       toFileResponses(g.Files),
		})
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{
		"groups":      groups,
		"next_cursor": page.NextCursor,
		"total":       page.Total,
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
func (s *Server) handleUploadFile(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	// 32 MiB in memory, rest spooled to disk. The request body is capped so
	// the spool cannot grow without bound (media.WriteUpload enforces the same
	// limit at the store layer). A small margin absorbs multipart part
	// headers and form fields.
	limit := s.maxUploadBytes
	if limit <= 0 {
		limit = media.DefaultMaxUploadBytes
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit+(1<<20))
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, s.logger, requestIDOrEmpty(r), http.StatusRequestEntityTooLarge,
				CodePayloadTooLarge, "Upload exceeds the maximum allowed size.")
			return
		}
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusBadRequest,
			CodeBadRequest, "Invalid multipart form.")
		return
	}
	mf, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusBadRequest,
			CodeBadRequest, "Missing 'file' field in form.")
		return
	}
	defer func() { _ = mf.Close() }()

	destPath := r.FormValue("path")
	if destPath == "" {
		destPath = header.Filename
	}
	if !s.requireCap(w, r, u, parentFolderKey(lib.ID, destPath), authz.CapCreate) {
		return
	}

	svc, cleanup, ok := s.openMediaService(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	created, err := svc.WriteUpload(r.Context(), destPath, mf)
	if err != nil {
		s.writeMediaError(w, r, err)
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
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"files": toFileResponses(files)})
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
}

// duplicateGroupResponse is one duplicate group in the duplicates listing.
type duplicateGroupResponse struct {
	ContentHash string         `json:"content_hash"`
	SizeBytes   int64          `json:"size_bytes"`
	Files       []fileResponse `json:"files"`
}

// similarityGroupResponse is one visual-similarity group in the listing.
// Unlike a duplicate group there is no single hash the group shares — members
// are connected by a chain of near-enough distances — so only the files
// themselves are reported.
type similarityGroupResponse struct {
	Files []fileResponse `json:"files"`
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
