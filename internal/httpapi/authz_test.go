package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Jishnu-Prasad888/Cairn/internal/audit"
	"github.com/Jishnu-Prasad888/Cairn/internal/auth"
	"github.com/Jishnu-Prasad888/Cairn/internal/authz"
	"github.com/Jishnu-Prasad888/Cairn/internal/crypto"
	"github.com/Jishnu-Prasad888/Cairn/internal/db"
	"github.com/Jishnu-Prasad888/Cairn/internal/library"
)

// newAuthzTestServer wires the full dependency graph including the
// authorization service and a registered library. It returns the admin client
// and a logged-in non-admin viewer client (no grants created yet).
func newAuthzTestServer(t *testing.T) (http.Handler, *testClient, *testClient, string) {
	t.Helper()

	tmpDir := t.TempDir()
	pool, err := db.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	auditSvc := audit.New(pool, logger)
	authSvc := auth.NewService(pool, logger, auditSvc)
	authzSvc := authz.NewService(pool, logger, nil)
	libManager := library.NewManager(pool, logger, auditSvc, crypto.NewKeys(""))

	libRoot := filepath.Join(tmpDir, "library")
	if err := os.MkdirAll(libRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	lib, _, err := libManager.Register(context.Background(), libRoot, "Test Library")
	if err != nil {
		t.Fatalf("register library: %v", err)
	}

	handler := New(Dependencies{
		Logger:    logger,
		DB:        pool,
		Auth:      authSvc,
		Authz:     authzSvc,
		Libraries: libManager,
	}).Handler()

	admin := &testClient{handler: handler}
	admin.roundTrip(t, http.MethodPost, "/api/v1/auth/bootstrap",
		`{"username":"admin","password":"correct-horse-battery"}`)

	// Viewer account (non-admin) and its own session.
	create := admin.do(t, http.MethodPost, "/api/v1/users",
		map[string]string{"username": "viewer", "password": "viewer-password"})
	var cu struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &cu); err != nil {
		t.Fatalf("unmarshal created user: %v (body=%s)", err, create.Body.Bytes())
	}

	viewer := &testClient{handler: handler}
	viewer.roundTrip(t, http.MethodPost, "/api/v1/auth/login",
		`{"username":"viewer","password":"viewer-password"}`)

	return handler, admin, viewer, lib.ID
}

// rawUpload performs a multipart upload against the live endpoint and returns
// the recorder so callers can assert either success or a denial.
func rawUpload(t *testing.T, c *testClient, libID, path string) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filepath.Base(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write([]byte("hello cairn")); err != nil {
		t.Fatal(err)
	}
	if err := mw.WriteField("path", path); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/libraries/"+libID+"/files/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	for name, value := range c.cookies {
		req.AddCookie(&http.Cookie{Name: name, Value: value})
	}
	rec := httptest.NewRecorder()
	c.handler.ServeHTTP(rec, req)
	c.applySetCookies(rec.Result().Cookies())
	return rec
}

// uploadFile uploads a small file through the live upload endpoint.
func uploadFile(t *testing.T, c *testClient, libID, path string) string {
	t.Helper()
	rec := rawUpload(t, c, libID, path)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		File struct {
			ID string `json:"id"`
		} `json:"file"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal upload: %v", err)
	}
	return resp.File.ID
}

func grantCap(t *testing.T, c *testClient, libID, userID, key string, caps []string) {
	t.Helper()
	rec := c.do(t, http.MethodPost, "/api/v1/libraries/"+libID+"/permissions",
		map[string]any{
			"user_id": userID,
			"key":     key,
			"caps":    caps,
			"effect":  "allow",
		})
	if rec.Code != http.StatusCreated {
		t.Fatalf("grant status = %d, body=%s", rec.Code, rec.Body.String())
	}
}

func denyCap(t *testing.T, c *testClient, libID, userID, key string, caps []string) {
	t.Helper()
	rec := c.do(t, http.MethodPost, "/api/v1/libraries/"+libID+"/permissions",
		map[string]any{
			"user_id": userID,
			"key":     key,
			"caps":    caps,
			"effect":  "deny",
		})
	if rec.Code != http.StatusCreated {
		t.Fatalf("deny status = %d, body=%s", rec.Code, rec.Body.String())
	}
}

const viewerUser = "viewer"

func viewerID(t *testing.T, admin *testClient) string {
	t.Helper()
	rec := admin.do(t, http.MethodGet, "/api/v1/users", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list users status = %d", rec.Code)
	}
	var resp struct {
		Users []struct {
			ID       string `json:"id"`
			Username string `json:"username"`
		} `json:"users"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal users: %v", err)
	}
	for _, u := range resp.Users {
		if u.Username == viewerUser {
			return u.ID
		}
	}
	t.Fatal("viewer user not found")
	return ""
}

func TestAuthz_ReadGrantGrantsAccess(t *testing.T) {
	_, admin, viewer, libID := newAuthzTestServer(t)
	fileID := uploadFile(t, admin, libID, "photos/sunset.jpg")

	vid := viewerID(t, admin)
	grantCap(t, admin, libID, vid, authz.LibraryKey(libID), []string{"read"})

	// Viewer can list the library and fetch file metadata (read).
	rec := viewer.do(t, http.MethodGet,
		"/api/v1/libraries/"+libID+"/files?folder=photos", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("viewer list = %d, body=%s", rec.Code, rec.Body.String())
	}
	rec = viewer.do(t, http.MethodGet,
		"/api/v1/libraries/"+libID+"/files/"+fileID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("viewer get = %d, body=%s", rec.Code, rec.Body.String())
	}
	// Read is not download.
	rec = viewer.do(t, http.MethodGet,
		"/api/v1/libraries/"+libID+"/files/"+fileID+"/download", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("viewer download = %d, want 403", rec.Code)
	}
	// Read is not create.
	rec = rawUpload(t, viewer, libID, "photos/intruder.jpg")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("viewer upload = %d, want 403", rec.Code)
	}
	// Read is not manage: permissions and shares stay admin-only.
	rec = viewer.do(t, http.MethodGet,
		"/api/v1/libraries/"+libID+"/permissions", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("viewer list grants = %d, want 403", rec.Code)
	}
	rec = viewer.do(t, http.MethodPost,
		"/api/v1/libraries/"+libID+"/shares",
		map[string]any{"key": authz.LibraryKey(libID), "caps": []string{"read"}})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("viewer create share = %d, want 403", rec.Code)
	}
}

func TestAuthz_DenyOverridesInheritedAllow(t *testing.T) {
	_, admin, viewer, libID := newAuthzTestServer(t)
	uploadFile(t, admin, libID, "photos/sunset.jpg")
	uploadFile(t, admin, libID, "private/secret.jpg")

	vid := viewerID(t, admin)
	grantCap(t, admin, libID, vid, authz.LibraryKey(libID), []string{"read"})
	denyCap(t, admin, libID, vid, authz.FolderKey(libID, "private"), []string{"read"})

	// The library allow still applies everywhere except the denied subtree.
	rec := viewer.do(t, http.MethodGet,
		"/api/v1/libraries/"+libID+"/files?folder=photos", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("viewer list photos = %d, want 200", rec.Code)
	}
	rec = viewer.do(t, http.MethodGet,
		"/api/v1/libraries/"+libID+"/files?folder=private", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("viewer list private = %d, want 403", rec.Code)
	}
}

func TestAuthz_PermissionsRequireManage(t *testing.T) {
	_, admin, viewer, libID := newAuthzTestServer(t)
	vid := viewerID(t, admin)
	grantCap(t, admin, libID, vid, authz.LibraryKey(libID),
		[]string{"read", "manage"})

	// Manage now lets the viewer administer permissions (including self-grants).
	rec := viewer.do(t, http.MethodGet,
		"/api/v1/libraries/"+libID+"/permissions", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("viewer list grants = %d, want 200", rec.Code)
	}
}

func TestAuthz_PublicShareFlow(t *testing.T) {
	_, admin, _, libID := newAuthzTestServer(t)
	fileID := uploadFile(t, admin, libID, "photos/sunset.jpg")

	rec := admin.do(t, http.MethodPost, "/api/v1/libraries/"+libID+"/shares",
		map[string]any{
			"key":      authz.FolderKey(libID, "photos"),
			"caps":     []string{"read", "download"},
			"password": "sekrit-pass",
		})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create share = %d, body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal share: %v", err)
	}
	if resp.Token == "" {
		t.Fatal("share token is empty")
	}

	// Public requests carry no session; the password is required.
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/shares/"+resp.Token+"/files?folder=photos", nil)
	rr := httptest.NewRecorder()
	admin.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("share without password = %d, want 401", rr.Code)
	}

	// With the password the share lists its subtree.
	req = httptest.NewRequest(http.MethodGet,
		"/api/v1/shares/"+resp.Token+"/files?folder=photos", nil)
	req.Header.Set(sharePasswordHeader, "sekrit-pass")
	rr = httptest.NewRecorder()
	admin.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("share list = %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}

	// Download executes with read+download.
	req = httptest.NewRequest(http.MethodGet,
		"/api/v1/shares/"+resp.Token+"/files/"+fileID+"/download", nil)
	req.Header.Set(sharePasswordHeader, "sekrit-pass")
	rr = httptest.NewRecorder()
	admin.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("share download = %d, want 200", rr.Code)
	}
}

func TestAuthz_PublicShareReadOnlyHasNoDownload(t *testing.T) {
	_, admin, _, libID := newAuthzTestServer(t)
	fileID := uploadFile(t, admin, libID, "photos/sunset.jpg")

	rec := admin.do(t, http.MethodPost, "/api/v1/libraries/"+libID+"/shares",
		map[string]any{
			"key":  authz.FolderKey(libID, "photos"),
			"caps": []string{"read"},
		})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create share = %d, body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal share: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/shares/"+resp.Token+"/files/"+fileID+"/download", nil)
	rr := httptest.NewRecorder()
	admin.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("read-only share download = %d, want 403", rr.Code)
	}
}

func TestAuthz_RevokedShareIsImmediatelyDead(t *testing.T) {
	_, admin, _, libID := newAuthzTestServer(t)
	uploadFile(t, admin, libID, "photos/sunset.jpg")

	rec := admin.do(t, http.MethodPost, "/api/v1/libraries/"+libID+"/shares",
		map[string]any{
			"key":  authz.FolderKey(libID, "photos"),
			"caps": []string{"read"},
		})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create share = %d, body=%s", rec.Code, rec.Body.String())
	}
	var created struct {
		Share struct {
			ID string `json:"id"`
		} `json:"share"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal share: %v", err)
	}

	rec = admin.do(t, http.MethodDelete,
		"/api/v1/libraries/"+libID+"/shares/"+created.Share.ID, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("revoke share = %d, want 204", rec.Code)
	}

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/shares/"+created.Token, nil)
	rr := httptest.NewRecorder()
	admin.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("revoked share = %d, want 401", rr.Code)
	}
}
