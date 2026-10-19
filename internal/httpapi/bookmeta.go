package httpapi

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Jishnu-Prasad888/Orchard/internal/auth"
	"github.com/Jishnu-Prasad888/Orchard/internal/authz"
	"github.com/Jishnu-Prasad888/Orchard/internal/bookmeta"
	"github.com/Jishnu-Prasad888/Orchard/internal/library"
	"github.com/Jishnu-Prasad888/Orchard/internal/librarydb"
	"github.com/Jishnu-Prasad888/Orchard/internal/media"
)

// maxCoverUploadBytes bounds a single custom cover image. Covers are small
// by nature (a few hundred KB at most); this is generous headroom, not a
// target.
const maxCoverUploadBytes = 8 << 20 // 8 MiB

// openBookStore opens the per-library DB and returns a bookmeta.Store.
func (s *Server) openBookStore(
	w http.ResponseWriter, r *http.Request, lib *library.Library,
) (*bookmeta.Store, func(), bool) {
	if lib.Status == library.StatusOffline {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusConflict,
			CodeConflict, "Library is offline.")
		return nil, nil, false
	}
	db, release, err := librarydb.Acquire(lib.Root + "/.orchard")
	if err != nil {
		s.logger.Error("open library db for book metadata", "library_id", lib.ID, "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to open library database.")
		return nil, nil, false
	}
	return bookmeta.NewStore(db), release, true
}

// bookMetadataResponse is the wire representation of a file's book metadata.
type bookMetadataResponse struct {
	FileID      string `json:"file_id"`
	Title       string `json:"title"`
	Author      string `json:"author"`
	Publisher   string `json:"publisher"`
	Year        string `json:"year"`
	ISBN        string `json:"isbn"`
	Description string `json:"description"`
	CoverURL    string `json:"cover_url,omitempty"`
	UserEdited  bool   `json:"user_edited"`
	ExtractedAt string `json:"extracted_at,omitempty"`
}

func toBookMetadataResponse(lib *library.Library, m bookmeta.Metadata) bookMetadataResponse {
	resp := bookMetadataResponse{
		FileID:      m.FileID,
		Title:       m.Title,
		Author:      m.Author,
		Publisher:   m.Publisher,
		Year:        m.Year,
		ISBN:        m.ISBN,
		Description: m.Description,
		UserEdited:  m.UserEdited,
	}
	if m.CoverPath != "" {
		resp.CoverURL = coverURL(lib.ID, m.FileID, coverVersion(lib, m.CoverPath))
	}
	if !m.ExtractedAt.IsZero() {
		resp.ExtractedAt = m.ExtractedAt.UTC().Format(time.RFC3339)
	}
	return resp
}

// handleGetBookMetadata — GET /api/v1/libraries/{id}/files/{fileID}/book-metadata
func (s *Server) handleGetBookMetadata(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.fileKeyFor(w, r, u, lib, r.PathValue("fileID"), authz.CapRead) {
		return
	}
	store, cleanup, ok := s.openBookStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	m, _, err := store.Get(r.Context(), r.PathValue("fileID"))
	if err != nil {
		s.logger.Error("get book metadata", "library_id", lib.ID, "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to read book metadata.")
		return
	}
	m.FileID = r.PathValue("fileID")
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"book": toBookMetadataResponse(lib, m)})
}

// bookMetadataUpdateRequest is the accepted PATCH payload: every field is
// optional and independent, so a caller sends only what changed.
type bookMetadataUpdateRequest struct {
	Title       *string `json:"title"`
	Author      *string `json:"author"`
	Publisher   *string `json:"publisher"`
	Year        *string `json:"year"`
	ISBN        *string `json:"isbn"`
	Description *string `json:"description"`
}

// handleUpdateBookMetadata — PATCH /api/v1/libraries/{id}/files/{fileID}/book-metadata
func (s *Server) handleUpdateBookMetadata(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.fileKeyFor(w, r, u, lib, r.PathValue("fileID"), authz.CapEdit) {
		return
	}
	var body bookMetadataUpdateRequest
	if err := readJSON(w, r, &body); err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), err)
		return
	}

	store, cleanup, ok := s.openBookStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()

	m, err := store.SetUserEdit(r.Context(), r.PathValue("fileID"), bookmeta.UserEdit{
		Title:       body.Title,
		Author:      body.Author,
		Publisher:   body.Publisher,
		Year:        body.Year,
		ISBN:        body.ISBN,
		Description: body.Description,
	})
	if err != nil {
		s.logger.Error("update book metadata", "library_id", lib.ID, "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to save book metadata.")
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"book": toBookMetadataResponse(lib, m)})
}

// handleGetCover — GET /api/v1/libraries/{id}/files/{fileID}/cover
func (s *Server) handleGetCover(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.fileKeyFor(w, r, u, lib, r.PathValue("fileID"), authz.CapRead) {
		return
	}
	store, cleanup, ok := s.openBookStore(w, r, lib)
	if !ok {
		return
	}
	m, _, err := store.Get(r.Context(), r.PathValue("fileID"))
	cleanup()
	if err != nil {
		s.logger.Error("get cover metadata", "library_id", lib.ID, "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to read cover.")
		return
	}
	if m.CoverPath == "" {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusNotFound, CodeNotFound, "No cover set.")
		return
	}

	full := filepath.Join(lib.Root, ".orchard", filepath.FromSlash(m.CoverPath))
	f, err := os.Open(full)
	if err != nil {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusNotFound, CodeNotFound, "Cover file is missing.")
		return
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError, CodeInternal, "Internal server error.")
		return
	}
	w.Header().Set("Content-Type", media.DetectMIME(m.CoverPath))
	// A versioned URL (see coverURL) names one exact image, so it can be
	// cached for good; a bare one must be re-checked, or a replaced cover
	// would keep showing its predecessor.
	if r.URL.Query().Get("v") != "" {
		w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "private, no-cache")
	}
	http.ServeContent(w, r, filepath.Base(m.CoverPath), info.ModTime(), f)
}

// coverExtFromContentType maps an uploaded cover's declared Content-Type to
// a file extension, so the stored file keeps a type-appropriate name.
func coverExtFromContentType(ct string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(strings.SplitN(ct, ";", 2)[0])) {
	case "image/jpeg", "image/jpg":
		return ".jpg", true
	case "image/png":
		return ".png", true
	case "image/webp":
		return ".webp", true
	case "image/gif":
		return ".gif", true
	default:
		return "", false
	}
}

// handleUploadCover — PUT /api/v1/libraries/{id}/files/{fileID}/cover
// Body is the raw image bytes; Content-Type decides the stored extension.
func (s *Server) handleUploadCover(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.fileKeyFor(w, r, u, lib, r.PathValue("fileID"), authz.CapEdit) {
		return
	}
	ext, ok := coverExtFromContentType(r.Header.Get("Content-Type"))
	if !ok {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusBadRequest, CodeBadRequest,
			"Unsupported image type. Use JPEG, PNG, WebP, or GIF.")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxCoverUploadBytes)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			writeError(w, s.logger, requestIDOrEmpty(r), http.StatusRequestEntityTooLarge,
				CodePayloadTooLarge, "Cover image is too large.")
			return
		}
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusBadRequest, CodeBadRequest, "Invalid request body.")
		return
	}
	if len(data) == 0 {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusBadRequest, CodeBadRequest, "Empty cover image.")
		return
	}

	fileID := r.PathValue("fileID")
	relCoverPath := filepath.ToSlash(filepath.Join("covers", fileID+ext))
	full := filepath.Join(lib.Root, ".orchard", filepath.FromSlash(relCoverPath))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		s.logger.Error("create covers dir", "library_id", lib.ID, "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError, CodeInternal, "Internal server error.")
		return
	}
	if err := os.WriteFile(full, data, 0o644); err != nil {
		s.logger.Error("write cover", "library_id", lib.ID, "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError, CodeInternal, "Internal server error.")
		return
	}

	store, cleanup, ok := s.openBookStore(w, r, lib)
	if !ok {
		return
	}
	defer cleanup()
	m, err := store.SetCover(r.Context(), fileID, relCoverPath)
	if err != nil {
		s.logger.Error("set cover", "library_id", lib.ID, "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError, CodeInternal, "Internal server error.")
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"book": toBookMetadataResponse(lib, m)})
}
