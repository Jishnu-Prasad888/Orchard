package httpapi

import (
	"bytes"
	"net/http"
	"path/filepath"

	"github.com/Jishnu-Prasad888/Cairn/internal/auth"
	"github.com/Jishnu-Prasad888/Cairn/internal/authz"
	"github.com/Jishnu-Prasad888/Cairn/internal/librarydb"
	"github.com/Jishnu-Prasad888/Cairn/internal/media"
	"github.com/Jishnu-Prasad888/Cairn/internal/metadata"
)

// handleGetFileMetadata — GET /api/v1/libraries/{id}/files/{fileID}/metadata
// Returns extracted media metadata (dimensions, EXIF, GPS) for a file.
func (s *Server) handleGetFileMetadata(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}

	cairnDir := filepath.Join(lib.Root, ".cairn")
	db, err := librarydb.Open(cairnDir)
	if err != nil {
		s.logger.Error("open library db", "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to open library database.")
		return
	}
	defer func() { _ = db.Close() }()

	store := media.NewFileStore(db, lib.ID)
	f, err := store.GetByID(r.Context(), r.PathValue("fileID"))
	if err != nil {
		s.writeMediaError(w, r, err)
		return
	}
	if !s.requireCap(w, r, u, authz.FileKey(lib.ID, f.RelPath), authz.CapRead) {
		return
	}

	// Return persisted metadata from media_metadata table.
	row := db.QueryRowContext(r.Context(),
		`SELECT media_type, mime_type, width, height, duration_secs, taken_at,
		        camera_make, camera_model, latitude, longitude, has_thumbnail, updated_at
		 FROM media_metadata WHERE file_id = ?`, f.ID)

	type metaResp struct {
		FileID       string   `json:"file_id"`
		MediaType    string   `json:"media_type,omitempty"`
		MIMEType     string   `json:"mime_type,omitempty"`
		Width        *int     `json:"width,omitempty"`
		Height       *int     `json:"height,omitempty"`
		DurationSecs *float64 `json:"duration_secs,omitempty"`
		TakenAt      string   `json:"taken_at,omitempty"`
		CameraMake   string   `json:"camera_make,omitempty"`
		CameraModel  string   `json:"camera_model,omitempty"`
		Latitude     *float64 `json:"latitude,omitempty"`
		Longitude    *float64 `json:"longitude,omitempty"`
		HasThumbnail bool     `json:"has_thumbnail"`
		UpdatedAt    string   `json:"updated_at,omitempty"`
	}

	var (
		resp         metaResp
		mimeType     nullableString
		width        nullableInt
		height       nullableInt
		durationSecs nullableFloat
		takenAt      nullableString
		cameraMake   nullableString
		cameraModel  nullableString
		lat          nullableFloat
		lon          nullableFloat
		hasThumbnail int
		updatedAt    string
	)
	resp.FileID = f.ID

	scanErr := row.Scan(
		&resp.MediaType, &mimeType, &width, &height, &durationSecs,
		&takenAt, &cameraMake, &cameraModel, &lat, &lon,
		&hasThumbnail, &updatedAt,
	)
	if scanErr != nil {
		// No metadata yet — return minimal response with file info.
		resp.MediaType = string(f.MediaType)
		resp.MIMEType = f.MIMEType
		resp.HasThumbnail = false
		writeJSON(w, s.logger, http.StatusOK, map[string]any{"metadata": resp})
		return
	}

	resp.MIMEType = mimeType.String
	if width.Valid {
		v := int(width.Int64)
		resp.Width = &v
	}
	if height.Valid {
		v := int(height.Int64)
		resp.Height = &v
	}
	if durationSecs.Valid {
		resp.DurationSecs = &durationSecs.Float64
	}
	resp.TakenAt = takenAt.String
	resp.CameraMake = cameraMake.String
	resp.CameraModel = cameraModel.String
	if lat.Valid {
		resp.Latitude = &lat.Float64
	}
	if lon.Valid {
		resp.Longitude = &lon.Float64
	}
	resp.HasThumbnail = hasThumbnail == 1
	resp.UpdatedAt = updatedAt

	writeJSON(w, s.logger, http.StatusOK, map[string]any{"metadata": resp})
}

// handleGetThumbnail — GET /api/v1/libraries/{id}/files/{fileID}/thumbnail
// Serves the pre-generated thumbnail, or triggers on-demand generation if
// not yet available.
func (s *Server) handleGetThumbnail(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}

	cairnDir := filepath.Join(lib.Root, ".cairn")
	fileID := r.PathValue("fileID")

	// Resolve the file so the capability check runs on its resource key even
	// when the thumbnail already exists on disk.
	db, err := librarydb.Open(cairnDir)
	if err != nil {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to open library database.")
		return
	}
	defer func() { _ = db.Close() }()

	store := media.NewFileStore(db, lib.ID)
	f, err := store.GetByID(r.Context(), fileID)
	if err != nil {
		s.writeMediaError(w, r, err)
		return
	}
	if !s.requireCap(w, r, u, authz.FileKey(lib.ID, f.RelPath), authz.CapRead) {
		return
	}

	// Serve an existing thumbnail (decrypted when at-rest encryption is on).
	data, modTime, err := metadata.ReadThumb(cairnDir, fileID, s.keys)
	if err == nil {
		w.Header().Set("Content-Type", "image/jpeg")
		http.ServeContent(w, r, fileID+".jpg", modTime, bytes.NewReader(data))
		return
	}

	// Thumbnail not found — try on-demand generation.
	absPath := filepath.Join(lib.Root, filepath.FromSlash(f.RelPath))
	generated, err := metadata.GenerateThumbnail(absPath, cairnDir, fileID, s.keys)
	if err != nil || !generated {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusNotFound,
			CodeNotFound, "Thumbnail not available for this file.")
		return
	}

	// Serve the freshly generated thumbnail.
	data, modTime, err = metadata.ReadThumb(cairnDir, fileID, s.keys)
	if err != nil {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to open generated thumbnail.")
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	http.ServeContent(w, r, fileID+".jpg", modTime, bytes.NewReader(data))
}

// --- nullable scan helpers ---

type nullableString struct {
	String string
	Valid  bool
}

func (n *nullableString) Scan(value any) error {
	if value == nil {
		n.Valid = false
		return nil
	}
	switch v := value.(type) {
	case string:
		n.String = v
		n.Valid = true
	case []byte:
		n.String = string(v)
		n.Valid = true
	}
	return nil
}

type nullableInt struct {
	Int64 int64
	Valid bool
}

func (n *nullableInt) Scan(value any) error {
	if value == nil {
		n.Valid = false
		return nil
	}
	switch v := value.(type) {
	case int64:
		n.Int64 = v
		n.Valid = true
	case float64:
		n.Int64 = int64(v)
		n.Valid = true
	}
	return nil
}

type nullableFloat struct {
	Float64 float64
	Valid   bool
}

func (n *nullableFloat) Scan(value any) error {
	if value == nil {
		n.Valid = false
		return nil
	}
	switch v := value.(type) {
	case float64:
		n.Float64 = v
		n.Valid = true
	case int64:
		n.Float64 = float64(v)
		n.Valid = true
	}
	return nil
}

// handlePutThumbnail — PUT /api/v1/libraries/{id}/files/{fileID}/thumbnail
// Stores a video's poster frame captured by the client, for servers without
// ffmpeg. Only videos accept one, only when no thumbnail exists yet, and only
// from someone who may edit the file: the poster is shown to every viewer.
// The body (JPEG, PNG or GIF) is decoded and re-encoded, never stored as sent.
func (s *Server) handlePutThumbnail(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}

	cairnDir := filepath.Join(lib.Root, ".cairn")
	fileID := r.PathValue("fileID")
	db, err := librarydb.Open(cairnDir)
	if err != nil {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to open library database.")
		return
	}
	defer func() { _ = db.Close() }()

	store := media.NewFileStore(db, lib.ID)
	f, err := store.GetByID(r.Context(), fileID)
	if err != nil {
		s.writeMediaError(w, r, err)
		return
	}
	if !s.requireCap(w, r, u, authz.FileKey(lib.ID, f.RelPath), authz.CapRead, authz.CapEdit) {
		return
	}
	if f.MediaType != media.MediaTypeVideo {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusBadRequest,
			CodeBadRequest, "Only videos accept a captured thumbnail.")
		return
	}

	stored, err := metadata.StorePoster(http.MaxBytesReader(w, r.Body, metadata.MaxPosterBytes),
		cairnDir, f.ID, s.keys)
	if err != nil {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusBadRequest,
			CodeBadRequest, "The thumbnail could not be read as an image.")
		return
	}
	if stored {
		if _, err := db.ExecContext(r.Context(),
			`UPDATE media_metadata SET has_thumbnail = 1 WHERE file_id = ?`, f.ID); err != nil {
			s.logger.Warn("mark poster thumbnail", "file_id", f.ID, "error", err)
		}
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"stored": stored})
}
