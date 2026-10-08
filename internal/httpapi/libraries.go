package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/Jishnu-Prasad888/Cairn/internal/auth"
	"github.com/Jishnu-Prasad888/Cairn/internal/authz"
	"github.com/Jishnu-Prasad888/Cairn/internal/library"
)

// libraryRequest is the accepted create/adopt payload.
type libraryRequest struct {
	Path string `json:"path"`
	Name string `json:"name,omitempty"`
}

// probeRequest is the accepted probe payload.
type probeRequest struct {
	Path string `json:"path"`
}

// refreshRequest is the accepted refresh/reconnect payload; Path is optional
// and, when supplied, reconnects the library at the new location.
type refreshRequest struct {
	Path string `json:"path,omitempty"`
}

// toLibraryResponse is the API representation of a library without internal ids.
type libraryResponse struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Root          string `json:"root"`
	Status        string `json:"status"`
	VolumeID      string `json:"volume_id,omitempty"`
	SchemaVersion int    `json:"schema_version"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

func toLibraryResponse(l *library.Library) libraryResponse {
	return libraryResponse{
		ID:            l.ID,
		Name:          l.Name,
		Root:          l.Root,
		Status:        string(l.Status),
		VolumeID:      l.VolumeID,
		SchemaVersion: l.SchemaVersion,
		CreatedAt:     l.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:     l.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
}

// actorCtx stamps the request context with the authenticated principal so
// library audit records capture who acted. Handlers use it for every call.
func actorCtx(r *http.Request, u *auth.User) *http.Request {
	if u == nil {
		return r
	}
	return r.WithContext(library.WithActor(r.Context(), u.ID))
}

// handleProbeLibrary inspects a candidate path without registering it. Admin
// only; read-only on both the filesystem and the registrar.
func (s *Server) handleProbeLibrary(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var body probeRequest
	if err := readJSON(w, r, &body); err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), err)
		return
	}
	result, err := s.libraries.Probe(actorCtx(r, u).Context(), body.Path)
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"probe": result})
}

// handleCreateLibrary registers a library, creating fresh metadata or adopting
// an existing one. Admin only.
//
// Registering a library also queues its first scan. A library is registered
// precisely so its files can be seen, and a library that has never been scanned
// has no rows in indexed_files, so every page came up empty until an
// administrator noticed and clicked Re-index. The scan is queued in the
// background: the response does not wait for it, and a library whose scan fails
// to queue is still registered and can be re-triggered.
func (s *Server) handleCreateLibrary(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var body libraryRequest
	if err := readJSON(w, r, &body); err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), err)
		return
	}
	lib, mode, err := s.libraries.Register(actorCtx(r, u).Context(), body.Path, body.Name)
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}

	indexing := false
	if s.indexer != nil {
		if _, err := s.indexer.TriggerScan(actorCtx(r, u).Context(), lib.ID, lib.Root); err != nil {
			s.logger.Error("trigger initial index", "library_id", lib.ID, "error", err)
		} else {
			indexing = true
		}
	}

	writeJSON(w, s.logger, http.StatusCreated, map[string]any{
		"library":  toLibraryResponse(lib),
		"mode":     mode,
		"indexing": indexing,
	})
}

// handleListLibraries returns the registered libraries the caller may use.
//
// Administrators see every library. Everyone else sees only the libraries
// where they hold the read capability, so a member account can discover the
// libraries it has been granted instead of hitting 403 on every content route.
// This is the same resource-based decision every other handler makes through
// requireCap; it never leaks the existence of a library the caller cannot read.
func (s *Server) handleListLibraries(w http.ResponseWriter, r *http.Request, u *auth.User) {
	libs, err := s.libraries.List(r.Context())
	if err != nil {
		s.logger.Error("list libraries", "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Internal server error.")
		return
	}
	isAdmin := u != nil && u.Role == auth.RoleAdmin
	resp := make([]libraryResponse, 0, len(libs))
	for i := range libs {
		if !isAdmin && !s.canReadLibrary(r, u, libs[i].ID) {
			continue
		}
		resp = append(resp, toLibraryResponse(&libs[i]))
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"libraries": resp})
}

// canReadLibrary reports whether the principal holds read on the library scope.
// A failing authorization subsystem is treated as "not readable" here so a
// database error narrows the listing instead of widening it; the individual
// content endpoints remain the authority and will surface the real error.
func (s *Server) canReadLibrary(r *http.Request, u *auth.User, libID string) bool {
	if s.authz == nil {
		// No authorization service wired: fall back to the pre-Phase-9 gate.
		return u != nil && u.Role == auth.RoleAdmin
	}
	ok, err := s.authz.Can(r.Context(), principalFrom(u), authz.LibraryKey(libID), authz.CapRead)
	if err != nil {
		s.logger.Error("list libraries", "library", libID, "error", err)
		return false
	}
	return ok
}

// handleGetLibrary returns a single library. Requires read on the library
// resource.
func (s *Server) handleGetLibrary(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapRead) {
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"library": toLibraryResponse(lib)})
}

// handleRefreshLibrary rechecks connectivity or, when a new path is supplied,
// reconnects the library there. Admin only.
func (s *Server) handleRefreshLibrary(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var body refreshRequest
	if err := readJSON(w, r, &body); err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), err)
		return
	}
	lib, err := s.libraries.Refresh(actorCtx(r, u).Context(), r.PathValue("id"), body.Path)
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"library": toLibraryResponse(lib)})
}

// handleDeleteLibrary unregisters a library, leaving its .cairn metadata in
// place. Admin only.
func (s *Server) handleDeleteLibrary(w http.ResponseWriter, r *http.Request, u *auth.User) {
	if err := s.libraries.Remove(actorCtx(r, u).Context(), r.PathValue("id")); err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeLibraryError maps library domain errors onto the JSON error envelope.
func (s *Server) writeLibraryError(w http.ResponseWriter, r *http.Request, err error) {
	requestID := requestIDOrEmpty(r)
	var ve *library.ValidationError
	switch {
	case errors.As(err, &ve):
		writeError(w, s.logger, requestID, http.StatusBadRequest, CodeBadRequest, ve.Error())
	case errors.Is(err, library.ErrExists):
		writeError(w, s.logger, requestID, http.StatusConflict, CodeConflict,
			"A library is already registered at that path.")
	case errors.Is(err, library.ErrNotFound):
		writeError(w, s.logger, requestID, http.StatusNotFound, CodeNotFound, "Library not found.")
	case errors.Is(err, library.ErrNoMetadata):
		writeError(w, s.logger, requestID, http.StatusBadRequest, CodeBadRequest,
			"No Cairn metadata was found at that path.")
	default:
		s.logger.Error("library error", "error", err)
		writeError(w, s.logger, requestID, http.StatusInternalServerError, CodeInternal,
			"Internal server error.")
	}
}
