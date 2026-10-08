package httpapi

import (
	"context"
	"errors"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Jishnu-Prasad888/Cairn/internal/auth"
	"github.com/Jishnu-Prasad888/Cairn/internal/authz"
	"github.com/Jishnu-Prasad888/Cairn/internal/library"
	"github.com/Jishnu-Prasad888/Cairn/internal/librarydb"
	"github.com/Jishnu-Prasad888/Cairn/internal/media"
	"github.com/Jishnu-Prasad888/Cairn/internal/ml"
	"github.com/Jishnu-Prasad888/Cairn/internal/safeimage"
)

// facesLib resolves the library and requires the face capability to be on.
func (s *Server) facesLib(w http.ResponseWriter, r *http.Request, u *auth.User, requireEnabled bool) (*library.Library, bool) {
	if s.faces == nil {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusServiceUnavailable,
			CodeServiceUnavailable, "Face recognition is not available.")
		return nil, false
	}
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return nil, false
	}
	if requireEnabled && !s.faces.Enabled() {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusServiceUnavailable,
			CodeServiceUnavailable, "Face recognition is disabled.")
		return nil, false
	}
	return lib, true
}

// handleFaceStatus — GET /api/v1/libraries/{id}/ml/faces
// Reports the face capability state for the library (faces, people, provider).
func (s *Server) handleFaceStatus(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, ok := s.facesLib(w, r, u, true)
	if !ok {
		return
	}
	st, err := s.faces.Status(r.Context(), lib.Root)
	if err != nil {
		s.logger.Error("face status", "library_id", lib.ID, "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to retrieve face status.")
		return
	}
	writeJSON(w, s.logger, http.StatusOK, st)
}

// runFacePass runs a face pass detached from the request so a slow pass is
// not cancelled when the handler returns; bound with a generous timeout.
func (s *Server) runFacePass(w http.ResponseWriter, r *http.Request, lib *library.Library, phase string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		if strings.Contains(phase, "cluster") {
			if _, err := s.faces.ClusterPass(ctx, lib.Root); err != nil {
				s.logger.Error("face clustering pass", "library_id", lib.ID, "error", err)
			}
			return
		}
		if _, err := s.faces.Pass(ctx, lib.Root); err != nil {
			s.logger.Error("face detection pass", "library_id", lib.ID, "error", err)
			return
		}
		// New faces are only useful once grouped into people.
		if _, err := s.faces.ClusterPass(ctx, lib.Root); err != nil {
			s.logger.Error("face clustering pass", "library_id", lib.ID, "error", err)
		}
	}()
	writeJSON(w, s.logger, http.StatusAccepted, map[string]any{
		"library_id": lib.ID,
		"status":     phase + " pass started",
	})
}

// handleFacePass — POST /api/v1/libraries/{id}/ml/faces/pass
func (s *Server) handleFacePass(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, ok := s.facesLib(w, r, u, true)
	if !ok {
		return
	}
	s.runFacePass(w, r, lib, "face detection")
}

// handleFaceCluster — POST /api/v1/libraries/{id}/ml/faces/cluster
func (s *Server) handleFaceCluster(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, ok := s.facesLib(w, r, u, true)
	if !ok {
		return
	}
	s.runFacePass(w, r, lib, "face clustering")
}

// handleFacePurge — POST /api/v1/libraries/{id}/ml/faces/purge
// Deletes every derived face and assignment. Originals and names survive.
func (s *Server) handleFacePurge(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, ok := s.facesLib(w, r, u, true)
	if !ok {
		return
	}
	n, err := s.faces.Purge(r.Context(), lib.Root)
	if err != nil {
		s.logger.Error("faces purge", "library_id", lib.ID, "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to purge face data.")
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{
		"library_id":    lib.ID,
		"faces_removed": n,
	})
}

// faceSummary trims a face to its API shape (no descriptor blob).
func faceSummary(f ml.FaceRecord) map[string]any {
	return map[string]any{
		"id":         f.ID,
		"file_id":    f.FileID,
		"file_path":  f.RelPath,
		"x":          f.Box.X,
		"y":          f.Box.Y,
		"width":      f.Box.Width,
		"height":     f.Box.Height,
		"confidence": f.Box.Confidence,
	}
}

// handleListPeople — GET /api/v1/libraries/{id}/people
func (s *Server) handleListPeople(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, ok := s.facesLib(w, r, u, true)
	if !ok || !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapRead) {
		return
	}
	people, err := s.faces.People(r.Context(), lib.Root)
	if err != nil {
		s.writeFaceError(w, r, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"people": people})
}

// handleCreatePerson — POST /api/v1/libraries/{id}/people
func (s *Server) handleCreatePerson(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, ok := s.facesLib(w, r, u, true)
	if !ok || !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapCreate) {
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), err)
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusBadRequest,
			CodeBadRequest, "Person name is required.")
		return
	}
	p, err := s.faces.CreatePerson(r.Context(), lib.Root, name)
	if err != nil {
		s.writeFaceError(w, r, err)
		return
	}
	writeJSON(w, s.logger, http.StatusCreated, map[string]any{"person": p})
}

// handleGetPerson — GET /api/v1/libraries/{id}/people/{personID}
func (s *Server) handleGetPerson(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, ok := s.facesLib(w, r, u, true)
	if !ok || !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapRead) {
		return
	}
	personID := r.PathValue("personID")
	people, err := s.faces.People(r.Context(), lib.Root)
	if err != nil {
		s.writeFaceError(w, r, err)
		return
	}
	var found *ml.Person
	for i := range people {
		if people[i].ID == personID {
			found = &people[i]
			break
		}
	}
	if found == nil {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusNotFound,
			CodeNotFound, "Person not found.")
		return
	}
	faces, err := s.faces.PersonFaces(r.Context(), lib.Root, personID)
	if err != nil {
		s.writeFaceError(w, r, err)
		return
	}
	summaries := make([]map[string]any, 0, len(faces))
	for _, f := range faces {
		summaries = append(summaries, faceSummary(f))
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{
		"person": found,
		"faces":  summaries,
	})
}

// handleRenamePerson — POST /api/v1/libraries/{id}/people/{personID}/rename
func (s *Server) handleRenamePerson(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, ok := s.facesLib(w, r, u, true)
	if !ok || !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapEdit) {
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), err)
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusBadRequest,
			CodeBadRequest, "Person name is required.")
		return
	}
	if err := s.faces.RenamePerson(r.Context(), lib.Root, r.PathValue("personID"), name); err != nil {
		s.writeFaceError(w, r, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"renamed": true})
}

// handleDeletePerson — DELETE /api/v1/libraries/{id}/people/{personID}
func (s *Server) handleDeletePerson(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, ok := s.facesLib(w, r, u, true)
	if !ok || !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapDelete) {
		return
	}
	personID := r.PathValue("personID")
	// ?faces=delete removes the person's faces too; otherwise they return to
	// the pool and are grouped again.
	if r.URL.Query().Get("faces") == "delete" {
		n, err := s.faces.DeletePersonAndFaces(r.Context(), lib.Root, personID)
		if err != nil {
			s.writeFaceError(w, r, err)
			return
		}
		writeJSON(w, s.logger, http.StatusOK, map[string]any{"deleted": true, "faces_removed": n})
		return
	}
	if err := s.faces.DeletePerson(r.Context(), lib.Root, personID); err != nil {
		s.writeFaceError(w, r, err)
		return
	}
	s.faces.RegroupSoon(lib.Root)
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"deleted": true})
}

// handleDeleteFace — DELETE /api/v1/libraries/{id}/faces/{faceID}
// Removes one face for good: it is not detected again.
func (s *Server) handleDeleteFace(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, ok := s.facesLib(w, r, u, true)
	if !ok || !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapDelete) {
		return
	}
	if err := s.faces.DeleteFace(r.Context(), lib.Root, r.PathValue("faceID")); err != nil {
		s.writeFaceError(w, r, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"deleted": true})
}

// handleSetPersonCover — POST /api/v1/libraries/{id}/people/{personID}/cover
func (s *Server) handleSetPersonCover(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, ok := s.facesLib(w, r, u, true)
	if !ok || !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapEdit) {
		return
	}
	var body struct {
		FaceID string `json:"face_id"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), err)
		return
	}
	if body.FaceID == "" {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusBadRequest,
			CodeBadRequest, "face_id is required.")
		return
	}
	if err := s.faces.SetCover(r.Context(), lib.Root, r.PathValue("personID"), body.FaceID); err != nil {
		s.writeFaceError(w, r, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"cover_set": true})
}

// handleMergePeople — POST /api/v1/libraries/{id}/people/{personID}/merge
func (s *Server) handleMergePeople(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, ok := s.facesLib(w, r, u, true)
	if !ok || !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapEdit) {
		return
	}
	var body struct {
		SourcePersonID string `json:"source_person_id"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), err)
		return
	}
	if body.SourcePersonID == "" {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusBadRequest,
			CodeBadRequest, "source_person_id is required.")
		return
	}
	if body.SourcePersonID == r.PathValue("personID") {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusBadRequest,
			CodeBadRequest, "Cannot merge a person into itself.")
		return
	}
	joined, err := s.faces.MergePerson(r.Context(), lib.Root, r.PathValue("personID"), body.SourcePersonID)
	if err != nil {
		s.writeFaceError(w, r, err)
		return
	}
	s.faces.RegroupSoon(lib.Root)
	// "consolidated" counts other unnamed groups that were so similar they were
	// folded in too.
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"merged": true, "consolidated": joined})
}

// handleListFaces — GET /api/v1/libraries/{id}/faces
// Lists unassigned faces (the pool the UI shows when clustering).
func (s *Server) handleListFaces(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, ok := s.facesLib(w, r, u, true)
	if !ok || !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapRead) {
		return
	}
	faces, err := s.faces.Unassigned(r.Context(), lib.Root)
	if err != nil {
		s.writeFaceError(w, r, err)
		return
	}
	summaries := make([]map[string]any, 0, len(faces))
	for _, f := range faces {
		summaries = append(summaries, faceSummary(f))
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"faces": summaries})
}

// handleAssignFace — POST /api/v1/libraries/{id}/people/{personID}/faces/{faceID}
func (s *Server) handleAssignFace(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, ok := s.facesLib(w, r, u, true)
	if !ok || !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapEdit) {
		return
	}
	if err := s.faces.AssignFace(r.Context(), lib.Root, r.PathValue("personID"), r.PathValue("faceID")); err != nil {
		s.writeFaceError(w, r, err)
		return
	}
	// The person now has a hand-placed face: let matching use it right away.
	s.faces.RegroupSoon(lib.Root)
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"assigned": true})
}

// handleUnassignFace — DELETE /api/v1/libraries/{id}/people/{personID}/faces/{faceID}
func (s *Server) handleUnassignFace(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, ok := s.facesLib(w, r, u, true)
	if !ok || !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapEdit) {
		return
	}
	if err := s.faces.UnassignFace(r.Context(), lib.Root, r.PathValue("personID"), r.PathValue("faceID")); err != nil {
		s.writeFaceError(w, r, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"unassigned": true})
}

// handleFaceImage — GET /api/v1/libraries/{id}/faces/{faceID}/image
// Serves a derived JPEG crop of the face, gated on read access to the owning
// file like a thumbnail.
func (s *Server) handleFaceImage(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, ok := s.facesLib(w, r, u, true)
	if !ok {
		return
	}
	faceID := r.PathValue("faceID")

	db, err := librarydb.Open(filepath.Join(lib.Root, ".cairn"))
	if err != nil {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to open library database.")
		return
	}
	defer func() { _ = db.Close() }()

	face, err := ml.NewFaceStore(db).FaceByID(r.Context(), faceID)
	if err != nil || face == nil || face.ID == "" {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusNotFound,
			CodeNotFound, "Face not found.")
		return
	}
	fs := media.NewFileStore(db, lib.ID)
	f, err := fs.GetByID(r.Context(), face.FileID)
	if err != nil {
		s.writeMediaError(w, r, err)
		return
	}
	if !s.requireCap(w, r, u, authz.FileKey(lib.ID, f.RelPath), authz.CapRead) {
		return
	}

	src, err := os.Open(filepath.Join(lib.Root, filepath.FromSlash(f.RelPath)))
	if err != nil {
		s.logger.Info("open face source", "library_id", lib.ID, "file", f.RelPath, "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusNotFound,
			CodeNotFound, "Source image for this face is unavailable.")
		return
	}
	defer func() { _ = src.Close() }()
	img, _, err := safeimage.Decode(src)
	if err != nil {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusNotFound,
			CodeNotFound, "Source image for this face is not decodable.")
		return
	}
	data, err := ml.FaceJPEG(img, face.Box, 384)
	if err != nil {
		s.logger.Error("face crop", "library_id", lib.ID, "face_id", faceID, "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Failed to render face crop.")
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(data)
}

// writeFaceError maps face-layer errors onto API responses.
func (s *Server) writeFaceError(w http.ResponseWriter, r *http.Request, err error) {
	if err == ml.ErrFacesDisabled {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusServiceUnavailable,
			CodeServiceUnavailable, "Face recognition is disabled.")
		return
	}
	if errors.Is(err, ml.ErrPersonNotFound) {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusNotFound,
			CodeNotFound, "Person not found.")
		return
	}
	s.logger.Error("faces", "error", err)
	writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
		CodeInternal, "Face operation failed.")
}
