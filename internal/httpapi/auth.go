package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/Jishnu-Prasad888/Cairn/internal/auth"
)

// sessionCookieName is the cookie carrying the opaque session token. It is
// HttpOnly (never readable by JavaScript) and SameSite=Lax, which together
// block cookie theft by XSS and most cross-site request forgery.
const sessionCookieName = "cairn_session"

// sessionCookieMaxAge seconds matches the 30-day session TTL.
const sessionCookieMaxAge = 30 * 24 * 60 * 60

// userRequest is the accepted login/bootstrap payload.
type userRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

// userResponse is the API representation of an account. It deliberately omits
// the password hash and any internal columns.
type userResponse struct {
	ID        string    `json:"id"`
	Username  string    `json:"username"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

func toUserResponse(u *auth.User) userResponse {
	return userResponse{
		ID:        u.ID,
		Username:  u.Username,
		Role:      string(u.Role),
		CreatedAt: u.CreatedAt.UTC(),
	}
}

// metaFrom extracts non-sensitive client context for audit and session
// tracking. r.RemoteAddr is intentionally used over trusting proxy headers.
func metaFrom(r *http.Request) auth.Meta {
	return auth.Meta{
		IPAddress: r.RemoteAddr,
		UserAgent: r.UserAgent(),
	}
}

// cookieSecure reports whether the session cookie must carry the Secure flag,
// either because TLS is terminated by Cairn itself or the operator configured
// CAIRN_COOKIE_SECURE for a TLS-terminating reverse proxy.
func (s *Server) cookieSecure(r *http.Request) bool {
	return s.secureCookies || r.TLS != nil
}

// setSessionCookie writes the session cookie without content-type assumptions.
func setSessionCookie(w http.ResponseWriter, token string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
		MaxAge:   sessionCookieMaxAge,
	})
}

// clearSessionCookie expires the session cookie.
func clearSessionCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
		MaxAge:   -1,
	})
}

// sessionTokenFrom extracts the opaque token from the session cookie.
func sessionTokenFrom(r *http.Request) string {
	c, err := r.Cookie(sessionCookieName)
	if err != nil {
		return ""
	}
	return c.Value
}

// currentUser resolves the request's principal from its session cookie and
// stores it in the context. It returns false when the request is not
// authenticated, in which case a 401 has already been written.
func (s *Server) currentUser(w http.ResponseWriter, r *http.Request) (*auth.User, bool) {
	sess, err := s.auth.Authenticate(r.Context(), sessionTokenFrom(r))
	if err != nil || sess == nil || sess.User == nil {
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusUnauthorized, CodeUnauthorized,
			"Authentication required.")
		return nil, false
	}
	return sess.User, true
}

// AllowFunc decides whether an authenticated principal may reach a handler.
// Resource-based authorization (ADR-0005) enforces per-handler capabilities;
// for now one sanctioned gate (admin vs any user) keeps role checks in a single place.
type AllowFunc func(*auth.User) bool

func allowAdmin(u *auth.User) bool { return u.Role == auth.RoleAdmin }

func allowAny(_ *auth.User) bool { return true }

// withAuth authenticates the request and, on success, calls next with the
// principal. Denied principals receive 403. This is the only place the HTTP
// layer decides authentication outcomes, so security-sensitive behavior stays
// reviewable in one file.
func (s *Server) withAuth(allow AllowFunc, next func(w http.ResponseWriter, r *http.Request, u *auth.User)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := s.currentUser(w, r)
		if !ok {
			return
		}
		if !allow(u) {
			writeError(w, s.logger, requestIDOrEmpty(r), http.StatusForbidden, CodeForbidden,
				"You do not have permission to perform this action.")
			return
		}
		next(w, r.WithContext(auth.WithUser(r.Context(), u)), u)
	}
}

// handleAuthStatus reports whether the server needs bootstrap and whether the
// request is already authenticated. It is public so the web UI can route to
// setup, login, or the application on load.
func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	var (
		userCount int
		err       error
	)
	if userCount, err = s.auth.UserCount(r.Context()); err != nil {
		s.logger.Error("auth status: count users", "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError, CodeInternal,
			"Internal server error.")
		return
	}

	resp := map[string]any{
		"bootstrap_required": userCount == 0,
		"authenticated":      false,
	}
	if sess, err := s.auth.Authenticate(r.Context(), sessionTokenFrom(r)); err == nil && sess.User != nil {
		resp["authenticated"] = true
		resp["user"] = toUserResponse(sess.User)
	}
	writeJSON(w, s.logger, http.StatusOK, resp)
}

// handleBootstrap creates the initial administrator account and starts a
// session. Public because there is nothing to authenticate against yet; the
// service rejects it with CONFLICT once any account exists.
func (s *Server) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	if !s.limitPublicAuth(w, r, "") {
		return
	}
	var body userRequest
	if err := readJSON(w, r, &body); err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), err)
		return
	}

	user, token, err := s.auth.Bootstrap(r.Context(), body.Username, body.Password, metaFrom(r))
	if err != nil {
		s.writeAuthError(w, r, err)
		return
	}

	setSessionCookie(w, token, s.cookieSecure(r))
	writeJSON(w, s.logger, http.StatusCreated, map[string]any{"user": toUserResponse(user)})
}

// handleLogin authenticates credentials and starts a session.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body userRequest
	if err := readJSON(w, r, &body); err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), err)
		return
	}

	ip := clientIP(r)
	if !s.limitPublicAuth(w, r, body.Username) {
		return
	}

	user, token, err := s.auth.Login(r.Context(), body.Username, body.Password, metaFrom(r))
	if err != nil {
		if _, atLimit := s.ratelimit.recordFailure(ip, body.Username); atLimit {
			s.logger.Warn("auth rate limit exceeded", "username", body.Username)
		}
		s.writeAuthError(w, r, err)
		return
	}

	s.ratelimit.recordSuccess(ip, body.Username)
	setSessionCookie(w, token, s.cookieSecure(r))
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"user": toUserResponse(user)})
}

// handleLogout revokes the current session and clears the cookie. Idempotent:
// logging out without a session is still a 204.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	sess, _ := s.auth.Authenticate(r.Context(), sessionTokenFrom(r))
	if sess != nil && sess.User != nil {
		// Carry the principal so the logout audit event records the actor.
		s.auth.Logout(auth.WithUser(r.Context(), sess.User), sess.ID, metaFrom(r))
	}
	clearSessionCookie(w, s.cookieSecure(r))
	w.WriteHeader(http.StatusNoContent)
}

// handleMe returns the authenticated principal.
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request, u *auth.User) {
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"user": toUserResponse(u)})
}

// handleListUsers lists accounts. Admin only.
func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request, _ *auth.User) {
	users, err := s.auth.ListUsers(r.Context())
	if err != nil {
		s.logger.Error("list users", "error", err)
		writeError(w, s.logger, requestIDOrEmpty(r), http.StatusInternalServerError, CodeInternal,
			"Internal server error.")
		return
	}
	resp := make([]userResponse, 0, len(users))
	for i := range users {
		resp = append(resp, toUserResponse(&users[i]))
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"users": resp})
}

// handleCreateUser creates a new account. Admin only.
func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request, _ *auth.User) {
	var body userRequest
	if err := readJSON(w, r, &body); err != nil {
		writeDomainError(w, s.logger, requestIDOrEmpty(r), err)
		return
	}
	if body.Role == "" {
		body.Role = string(auth.RoleUser)
	}

	user, err := s.auth.CreateUser(r.Context(), body.Username, body.Password, body.Role)
	if err != nil {
		s.writeAuthError(w, r, err)
		return
	}
	writeJSON(w, s.logger, http.StatusCreated, map[string]any{"user": toUserResponse(user)})
}

// handleRevokeUserSessions invalidates every session for a user. Admin only.
func (s *Server) handleRevokeUserSessions(w http.ResponseWriter, r *http.Request, _ *auth.User) {
	id := r.PathValue("id")
	if err := s.auth.RevokeUserSessions(r.Context(), id, metaFrom(r)); err != nil {
		s.writeAuthError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeAuthError maps auth service errors onto the error envelope without
// leaking whether a username, password, or account state caused a login
// failure.
func (s *Server) writeAuthError(w http.ResponseWriter, r *http.Request, err error) {
	requestID := requestIDOrEmpty(r)
	var ve *auth.ValidationError
	switch {
	case errors.As(err, &ve):
		writeError(w, s.logger, requestID, http.StatusBadRequest, CodeBadRequest, ve.Error())
	case errors.Is(err, auth.ErrConflict):
		writeError(w, s.logger, requestID, http.StatusConflict, CodeConflict, "That resource already exists.")
	case errors.Is(err, auth.ErrUnauthorized):
		writeError(w, s.logger, requestID, http.StatusUnauthorized, CodeUnauthorized,
			"Invalid username or password.")
	case errors.Is(err, auth.ErrUserNotFound):
		writeError(w, s.logger, requestID, http.StatusNotFound, CodeNotFound, "User not found.")
	default:
		s.logger.Error("auth error", "error", err)
		writeError(w, s.logger, requestID, http.StatusInternalServerError, CodeInternal,
			"Internal server error.")
	}
}
