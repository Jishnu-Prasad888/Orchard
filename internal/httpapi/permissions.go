package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/Jishnu-Prasad888/Cairn/internal/auth"
	"github.com/Jishnu-Prasad888/Cairn/internal/authz"
)

// grantRequest is the accepted grant creation payload.
type grantRequest struct {
	UserID string   `json:"user_id"`
	Key    string   `json:"key"`
	Caps   []string `json:"caps"`
	Effect string   `json:"effect"`
}

// shareRequest is the accepted share creation payload.
type shareRequest struct {
	Key       string   `json:"key"`
	Caps      []string `json:"caps"`
	Password  string   `json:"password,omitempty"`
	ExpiresAt string   `json:"expires_at,omitempty"`
}

// handleListGrants — GET /api/v1/libraries/{id}/permissions
// Lists the grants rooted anywhere in the library. Requires manage on the
// library to administer permissions; viewers are never exposed ACL detail.
func (s *Server) handleListGrants(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapManage) {
		return
	}

	grants, err := s.authz.ListGrants(r.Context(), authz.LibraryKey(lib.ID))
	if err != nil {
		s.logger.Error("list grants", "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Internal server error.")
		return
	}
	resp := make([]grantResponse, 0, len(grants))
	for _, g := range grants {
		resp = append(resp, toGrantResponse(g))
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"grants": resp})
}

// handleCreateGrant — POST /api/v1/libraries/{id}/permissions
// Requires manage on the library. Grants always carry the library prefix in
// their key; a key given without the library prefix is scoped to this library.
func (s *Server) handleCreateGrant(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapManage) {
		return
	}

	var body grantRequest
	if err := readJSON(w, r, &body); err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), err)
		return
	}
	caps, err := parseCaps(body.Caps)
	if err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), &authz.ValidationError{Field: "caps", Reason: err.Error()})
		return
	}
	effect := authz.Effect(body.Effect)
	if effect != authz.EffectAllow && effect != authz.EffectDeny {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), &authz.ValidationError{Field: "effect", Reason: `must be "allow" or "deny"`})
		return
	}

	key := normalizeLibraryKey(lib.ID, body.Key)
	created, err := s.authz.CreateGrant(r.Context(), authz.GrantInput{
		UserID:      body.UserID,
		ResourceKey: key,
		Caps:        caps,
		Effect:      effect,
		CreatedBy:   u.ID,
	})
	if err != nil {
		s.writeAuthzError(w, r, err)
		return
	}
	writeJSON(w, s.logger, http.StatusCreated, map[string]any{"grant": toGrantResponse(*created)})
}

// handleRevokeGrant — DELETE /api/v1/libraries/{id}/permissions/{grantID}
func (s *Server) handleRevokeGrant(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapManage) {
		return
	}

	// Only grants rooted in this library may be revoked through this route.
	grantID := r.PathValue("grantID")
	spec, err := s.authz.GrantByID(r.Context(), grantID)
	if err != nil {
		s.writeAuthzError(w, r, err)
		return
	}
	if !keyUnderLibrary(spec.ResourceKey, lib.ID) {
		s.writeForbidden(w, r)
		return
	}
	if err := s.authz.RevokeGrant(r.Context(), grantID, u.ID); err != nil {
		s.writeAuthzError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleListShares — GET /api/v1/libraries/{id}/shares
func (s *Server) handleListShares(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapManage) {
		return
	}

	shares, err := s.authz.ListShares(r.Context(), authz.LibraryKey(lib.ID))
	if err != nil {
		s.logger.Error("list shares", "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError,
			CodeInternal, "Internal server error.")
		return
	}
	resp := make([]shareResponse, 0, len(shares))
	for _, sh := range shares {
		resp = append(resp, toShareResponse(sh))
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"shares": resp})
}

// handleCreateShare — POST /api/v1/libraries/{id}/shares
// Requires manage; the raw token is returned exactly once.
func (s *Server) handleCreateShare(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapManage) {
		return
	}

	var body shareRequest
	if err := readJSON(w, r, &body); err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), err)
		return
	}
	caps, err := parseCaps(body.Caps)
	if err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), &authz.ValidationError{Field: "caps", Reason: err.Error()})
		return
	}

	var expires *time.Time
	if body.ExpiresAt != "" {
		t, err := time.Parse(time.RFC3339, body.ExpiresAt)
		if err != nil {
			writeDomainError(w, s.logger, requestIDOrEmpty(r),
				&authz.ValidationError{Field: "expires_at", Reason: `must be an RFC3339 timestamp`})
			return
		}
		expires = &t
	}

	view, err := s.authz.CreateShare(r.Context(), authz.NewShareInput{
		ResourceKey: normalizeLibraryKey(lib.ID, body.Key),
		Caps:        caps,
		Password:    body.Password,
		ExpiresAt:   expires,
		CreatedBy:   u.ID,
	})
	if err != nil {
		s.writeAuthzError(w, r, err)
		return
	}
	writeJSON(w, s.logger, http.StatusCreated, map[string]any{
		"share": toShareResponse(view.Share),
		"token": view.Token,
	})
}

// handleRevokeShare — DELETE /api/v1/libraries/{id}/shares/{shareID}
func (s *Server) handleRevokeShare(w http.ResponseWriter, r *http.Request, u *auth.User) {
	lib, err := s.libraries.Get(actorCtx(r, u).Context(), r.PathValue("id"))
	if err != nil {
		s.writeLibraryError(w, r, err)
		return
	}
	if !s.requireCap(w, r, u, authz.LibraryKey(lib.ID), authz.CapManage) {
		return
	}

	shareID := r.PathValue("shareID")
	sh, err := s.authz.ShareByID(r.Context(), shareID)
	if err != nil {
		s.writeAuthzError(w, r, err)
		return
	}
	if !keyUnderLibrary(sh.ResourceKey, lib.ID) {
		s.writeForbidden(w, r)
		return
	}
	if err := s.authz.RevokeShare(r.Context(), shareID, u.ID); err != nil {
		s.writeAuthzError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func parseCaps(raw []string) (authz.CapSet, error) {
	set, err := authz.CapSetFromStrings(raw)
	if err != nil {
		return nil, err
	}
	if len(set) == 0 {
		return nil, errors.New("at least one capability is required")
	}
	return set, nil
}

// normalizeLibraryKey scopes a caller-supplied key to the given library so a
// grant can never escape its library. A fully-qualified key (already
// library-prefixed) is accepted as-is when it belongs to the library.
func normalizeLibraryKey(libID, key string) string {
	lib := authz.LibraryKey(libID)
	if key == "" {
		return lib
	}
	if keyUnderLibrary(key, libID) {
		return key
	}
	return authz.FolderKey(libID, key)
}

func keyUnderLibrary(key, libID string) bool {
	lib := authz.LibraryKey(libID)
	return coversKey(lib, key)
}

type grantResponse struct {
	ID           string   `json:"id"`
	UserID       string   `json:"user_id"`
	ResourceKey  string   `json:"resource_key"`
	Capabilities []string `json:"capabilities"`
	Effect       string   `json:"effect"`
	CreatedAt    string   `json:"created_at"`
}

func toGrantResponse(g authz.Grant) grantResponse {
	return grantResponse{
		ID:           g.ID,
		UserID:       g.UserID,
		ResourceKey:  g.ResourceKey,
		Capabilities: capNames(g.Capabilities),
		Effect:       string(g.Effect),
		CreatedAt:    g.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
}

type shareResponse struct {
	ID           string   `json:"id"`
	ResourceKey  string   `json:"resource_key"`
	Capabilities []string `json:"capabilities"`
	HasPassword  bool     `json:"has_password"`
	ExpiresAt    *string  `json:"expires_at"`
	RevokedAt    *string  `json:"revoked_at"`
	CreatedAt    string   `json:"created_at"`
}

func toShareResponse(sh authz.Share) shareResponse {
	out := shareResponse{
		ID:           sh.ID,
		ResourceKey:  sh.ResourceKey,
		Capabilities: capNames(sh.Capabilities),
		HasPassword:  sh.PasswordHash.Valid,
		CreatedAt:    sh.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
	if sh.ExpiresAt != nil {
		v := sh.ExpiresAt.UTC().Format(time.RFC3339Nano)
		out.ExpiresAt = &v
	}
	if sh.RevokedAt != nil {
		v := sh.RevokedAt.UTC().Format(time.RFC3339Nano)
		out.RevokedAt = &v
	}
	return out
}

// writeAuthzError maps authorization service errors onto the error envelope.
func (s *Server) writeAuthzError(w http.ResponseWriter, r *http.Request, err error) {
	requestID := requestIDOrEmpty(r)
	var ve *authz.ValidationError
	switch {
	case errors.As(err, &ve):
		writeError(w, s.logger, requestID, http.StatusBadRequest, CodeBadRequest, ve.Error())
	case errors.Is(err, authz.ErrNotFound):
		writeError(w, s.logger, requestID, http.StatusNotFound, CodeNotFound, "Not found.")
	case errors.Is(err, authz.ErrUserNotFound):
		writeError(w, s.logger, requestID, http.StatusNotFound, CodeNotFound, "User not found.")
	case errors.Is(err, authz.ErrUnauthorized):
		writeError(w, s.logger, requestID, http.StatusUnauthorized, CodeUnauthorized,
			"Invalid share token.")
	default:
		s.logger.Error("authorization error", "error", err)
		writeError(w, s.logger, requestID, http.StatusInternalServerError, CodeInternal,
			"Internal server error.")
	}
}

// capNames renders capabilities in canonical order for response payloads.
func capNames(c authz.CapSet) []string {
	list := c.List()
	out := make([]string, len(list))
	for i, cap := range list {
		out[i] = string(cap)
	}
	return out
}
