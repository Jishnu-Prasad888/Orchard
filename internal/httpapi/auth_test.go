package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Jishnu-Prasad888/Cairn/internal/audit"
	"github.com/Jishnu-Prasad888/Cairn/internal/auth"
	"github.com/Jishnu-Prasad888/Cairn/internal/db"
)

// newAuthTestServer wires the full dependency graph (database, audit, auth)
// and returns the server handler plus a fresh client that carries cookies.
func newAuthTestServer(t *testing.T) (http.Handler, *testClient) {
	t.Helper()
	pool, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	auditSvc := audit.New(pool, logger)
	authSvc := auth.NewService(pool, logger, auditSvc)

	handler := New(Dependencies{Logger: logger, DB: pool, Auth: authSvc}).Handler()
	return handler, &testClient{handler: handler}
}

// testClient performs requests against the handler while carrying the cookies
// set by the server, mirroring a browser session.
type testClient struct {
	handler http.Handler
	cookies map[string]string
}

func (c *testClient) roundTrip(t *testing.T, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, http.NoBody)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	for name, value := range c.cookies {
		req.AddCookie(&http.Cookie{Name: name, Value: value})
	}
	rec := httptest.NewRecorder()
	c.handler.ServeHTTP(rec, req)
	c.applySetCookies(rec.Result().Cookies())
	return rec
}

func (c *testClient) applySetCookies(cookies []*http.Cookie) {
	if c.cookies == nil {
		c.cookies = map[string]string{}
	}
	for _, cookie := range cookies {
		if cookie.Value == "" {
			delete(c.cookies, cookie.Name)
			continue
		}
		c.cookies[cookie.Name] = cookie.Value
	}
}

// dropSession removes the session cookie from the client jar, simulating a
// user whose browser lost the cookie.
func (c *testClient) dropSession() {
	delete(c.cookies, sessionCookieName)
}

func bootstrap(t *testing.T, c *testClient) {
	t.Helper()
	rec := c.roundTrip(t, http.MethodPost, "/api/v1/auth/bootstrap",
		`{"username":"admin","password":"correct-horse-battery"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("bootstrap status = %d, body=%s", rec.Code, rec.Body.String())
	}
}

func TestAuthStatusBeforeAndAfterBootstrap(t *testing.T) {
	h, client := newAuthTestServer(t)

	rec := client.roundTrip(t, http.MethodGet, "/api/v1/auth/status", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		BootstrapRequired bool `json:"bootstrap_required"`
		Authenticated     bool `json:"authenticated"`
	}
	jsonUnmarshalRec(t, rec, &body)
	if !body.BootstrapRequired || body.Authenticated {
		t.Errorf("before bootstrap: %+v, want bootstrap_required=true authenticated=false", body)
	}

	bootstrap(t, client)
	rec = client.roundTrip(t, http.MethodGet, "/api/v1/auth/status", "")
	jsonUnmarshalRec(t, rec, &body)
	if body.BootstrapRequired {
		t.Errorf("after bootstrap: %+v, want bootstrap_required=false", body)
	}
	if !body.Authenticated {
		t.Errorf("after bootstrap: %+v, want authenticated=true (session cookie set)", body)
	}

	// A fresh client without the cookie sees the same bootstrap state but no
	// authentication.
	fresh := &testClient{handler: h}
	rec = fresh.roundTrip(t, http.MethodGet, "/api/v1/auth/status", "")
	jsonUnmarshalRec(t, rec, &body)
	if body.BootstrapRequired || body.Authenticated {
		t.Errorf("fresh client: %+v, want bootstrap_required=false authenticated=false", body)
	}
}

func TestBootstrapSetsSessionCookie(t *testing.T) {
	_, client := newAuthTestServer(t)
	rec := client.roundTrip(t, http.MethodPost, "/api/v1/auth/bootstrap",
		`{"username":"admin","password":"correct-horse-battery"}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	cookie := findSetCookie(t, rec, sessionCookieName)
	if !cookie.HttpOnly {
		t.Error("session cookie is not HttpOnly")
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax", cookie.SameSite)
	}
	if cookie.Secure {
		t.Error("cookie must not be Secure over plain HTTP without configuration")
	}
	if cookie.Value == "" {
		t.Error("session cookie has no value")
	}
	if _, ok := client.cookies[sessionCookieName]; !ok {
		t.Error("client holds no session cookie")
	}

	// The cookie grants access to protected endpoints.
	rec = client.roundTrip(t, http.MethodGet, "/api/v1/auth/me", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("me status = %d, want 200", rec.Code)
	}
}

func TestLoginLogoutRoundTrip(t *testing.T) {
	_, client := newAuthTestServer(t)
	bootstrap(t, client)
	client.dropSession()

	// Without a session, protected endpoints are 401.
	rec := client.roundTrip(t, http.MethodGet, "/api/v1/auth/me", "")
	assertErrorEnvelope(t, rec, http.StatusUnauthorized, CodeUnauthorized)

	// Valid login.
	rec = client.roundTrip(t, http.MethodPost, "/api/v1/auth/login",
		`{"username":"admin","password":"correct-horse-battery"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d, body=%s", rec.Code, rec.Body.String())
	}

	var me struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	rec = client.roundTrip(t, http.MethodGet, "/api/v1/auth/me", "")
	jsonUnmarshalRec(t, rec, &me)
	if me.User.ID == "" {
		t.Fatal("me returned no user id")
	}

	// Logout revokes the session.
	rec = client.roundTrip(t, http.MethodPost, "/api/v1/auth/logout", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d, want 204", rec.Code)
	}
	rec = client.roundTrip(t, http.MethodGet, "/api/v1/auth/me", "")
	assertErrorEnvelope(t, rec, http.StatusUnauthorized, CodeUnauthorized)
}

func TestLoginRejectsBadCredentials(t *testing.T) {
	_, client := newAuthTestServer(t)
	bootstrap(t, client)

	for _, body := range []string{
		`{"username":"admin","password":"wrong"}`,
		`{"username":"ghost","password":"correct-horse-battery"}`,
	} {
		rec := client.roundTrip(t, http.MethodPost, "/api/v1/auth/login", body)
		assertErrorEnvelope(t, rec, http.StatusUnauthorized, CodeUnauthorized)
		if strings.Contains(rec.Body.String(), "correct") {
			t.Error("response must not echo credential material")
		}
	}
}

func TestBootstrapTwiceIsConflict(t *testing.T) {
	_, client := newAuthTestServer(t)
	bootstrap(t, client)
	rec := client.roundTrip(t, http.MethodPost, "/api/v1/auth/bootstrap",
		`{"username":"other","password":"another-password"}`)
	assertErrorEnvelope(t, rec, http.StatusConflict, CodeConflict)
}

func TestUserManagementRequiresAdmin(t *testing.T) {
	_, client := newAuthTestServer(t)
	bootstrap(t, client)

	// The bootstrapped admin can create users and list them.
	rec := client.roundTrip(t, http.MethodPost, "/api/v1/users",
		`{"username":"alice","password":"s3cret-s3cret","role":"user"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create user status = %d, body=%s", rec.Code, rec.Body.String())
	}
	rec = client.roundTrip(t, http.MethodGet, "/api/v1/users", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list users status = %d", rec.Code)
	}

	// A non-admin session cannot manage users.
	var created struct {
		User struct {
			Username string `json:"username"`
		} `json:"user"`
	}
	rec = client.roundTrip(t, http.MethodPost, "/api/v1/users",
		`{"username":"bob","password":"s3cret-s3cret","role":"user"}`)
	jsonUnmarshalRec(t, rec, &created)
	if created.User.Username != "bob" {
		t.Fatalf("created user = %+v", created.User)
	}

	client.roundTrip(t, http.MethodPost, "/api/v1/auth/logout", "")
	rec = client.roundTrip(t, http.MethodPost, "/api/v1/auth/login",
		`{"username":"bob","password":"s3cret-s3cret"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("bob login status = %d, body=%s", rec.Code, rec.Body.String())
	}

	for _, req := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/v1/users", ""},
		{http.MethodPost, "/api/v1/users", `{"username":"charlie","password":"s3cret-s3cret","role":"user"}`},
	} {
		rec := client.roundTrip(t, req.method, req.path, req.body)
		assertErrorEnvelope(t, rec, http.StatusForbidden, CodeForbidden)
	}
}

func TestRevokeUserSessionsEndpoint(t *testing.T) {
	_, client := newAuthTestServer(t)
	bootstrap(t, client)

	var created struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	rec := client.roundTrip(t, http.MethodPost, "/api/v1/users",
		`{"username":"alice","password":"s3cret-s3cret","role":"user"}`)
	jsonUnmarshalRec(t, rec, &created)

	rec = client.roundTrip(t, http.MethodPost, "/api/v1/users/"+created.User.ID+"/sessions/revoke", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("revoke status = %d, body=%s", rec.Code, rec.Body.String())
	}

	// Unknown target -> 404.
	rec = client.roundTrip(t, http.MethodPost, "/api/v1/users/nope/sessions/revoke", "")
	assertErrorEnvelope(t, rec, http.StatusNotFound, CodeNotFound)
}

func TestCreateUserValidatesInput(t *testing.T) {
	_, client := newAuthTestServer(t)
	bootstrap(t, client)

	for _, body := range []string{
		`{"username":"","password":"s3cret-s3cret","role":"user"}`,
		`{"username":"bad name","password":"s3cret-s3cret","role":"user"}`,
		`{"username":"alice","password":"short","role":"user"}`,
		`{"username":"alice","password":"s3cret-s3cret","role":"owner"}`,
	} {
		rec := client.roundTrip(t, http.MethodPost, "/api/v1/users", body)
		assertErrorEnvelope(t, rec, http.StatusBadRequest, CodeBadRequest)
	}
}

func TestDuplicateUsernameIsConflict(t *testing.T) {
	_, client := newAuthTestServer(t)
	bootstrap(t, client)
	client.roundTrip(t, http.MethodPost, "/api/v1/users",
		`{"username":"alice","password":"s3cret-s3cret","role":"user"}`)
	rec := client.roundTrip(t, http.MethodPost, "/api/v1/users",
		`{"username":"alice","password":"s3cret-s3cret","role":"user"}`)
	assertErrorEnvelope(t, rec, http.StatusConflict, CodeConflict)
}

func TestAuthErrorsCarryRequestID(t *testing.T) {
	_, client := newAuthTestServer(t)
	rec := client.roundTrip(t, http.MethodPost, "/api/v1/auth/login",
		`{"username":"x","password":"y"}`)

	var env ErrorResponse
	jsonUnmarshalRec(t, rec, &env)
	if env.Error.RequestID == "" {
		t.Error("auth error response lacks request_id")
	}
	if rec.Header().Get("X-Request-ID") == "" {
		t.Error("missing X-Request-ID header")
	}
}

func TestResponsesDoNotLeakPasswordHash(t *testing.T) {
	_, client := newAuthTestServer(t)
	bootstrap(t, client)

	rec := client.roundTrip(t, http.MethodGet, "/api/v1/auth/me", "")
	if strings.Contains(rec.Body.String(), "password") || strings.Contains(rec.Body.String(), "$argon2") {
		t.Fatal("user response leaks credential material")
	}
	rec = client.roundTrip(t, http.MethodGet, "/api/v1/users", "")
	if strings.Contains(rec.Body.String(), "$argon2") {
		t.Fatal("user list leaks credential material")
	}
}

func TestLargeBodyRejected(t *testing.T) {
	_, client := newAuthTestServer(t)
	big := strings.Repeat("a", 2*1024*1024)
	rec := client.roundTrip(t, http.MethodPost, "/api/v1/auth/login",
		`{"username":"a","password":"`+big+`"}`)
	assertErrorEnvelope(t, rec, http.StatusRequestEntityTooLarge, CodePayloadTooLarge)
}

func TestLoginMethodNotAllowedReturnsEnvelope(t *testing.T) {
	_, client := newAuthTestServer(t)
	rec := client.roundTrip(t, http.MethodGet, "/api/v1/auth/login", "")
	assertErrorEnvelope(t, rec, http.StatusNotFound, CodeNotFound)
}

func findSetCookie(t *testing.T, rec *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("Set-Cookie %q not found in %q", name, rec.Header().Get("Set-Cookie"))
	return nil
}

func jsonUnmarshalRec(t *testing.T, rec *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), dst); err != nil {
		t.Fatalf("unmarshal %q: %v", rec.Body.String(), err)
	}
}
