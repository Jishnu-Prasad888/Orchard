package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Jishnu-Prasad888/Cairn/internal/audit"
	"github.com/Jishnu-Prasad888/Cairn/internal/auth"
	"github.com/Jishnu-Prasad888/Cairn/internal/crypto"
	"github.com/Jishnu-Prasad888/Cairn/internal/db"
	"github.com/Jishnu-Prasad888/Cairn/internal/library"
	"github.com/Jishnu-Prasad888/Cairn/internal/librarydb"
)

// --- shared test infrastructure ---

// newSearchTestServer creates a fully-wired server with auth and a test library.
// It returns the handler, an authenticated admin client, the library ID, and the
// library root (so tests can seed the per-library database).
func newSearchTestServer(t *testing.T) (http.Handler, *testClient, string, string) {
	t.Helper()
	return newSearchTestServerWithKeys(t, nil)
}

// newSearchTestServerWithKeys is newSearchTestServer with an optional at-rest
// encryption key injected into the HTTP server dependencies.
func newSearchTestServerWithKeys(t *testing.T, keys *crypto.Keys) (http.Handler, *testClient, string, string) {
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
	libManager := library.NewManager(pool, logger, auditSvc, crypto.NewKeys(""))

	// Register a test library.
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
		Libraries: libManager,
		Keys:      keys,
	}).Handler()

	client := &testClient{handler: handler}
	// Bootstrap admin.
	client.roundTrip(t, http.MethodPost, "/api/v1/auth/bootstrap",
		`{"username":"admin","password":"correct-horse-battery"}`)
	// Login.
	client.roundTrip(t, http.MethodPost, "/api/v1/auth/login",
		`{"username":"admin","password":"correct-horse-battery"}`)

	return handler, client, lib.ID, libRoot
}

// seedLibraryFile inserts an indexed file directly into the per-library DB.
func seedLibraryFile(t *testing.T, libRoot, id, relPath string, size int64) error {
	t.Helper()
	ldb, err := librarydb.Open(filepath.Join(libRoot, ".cairn"))
	if err != nil {
		return err
	}
	defer func() { _ = ldb.Close() }()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = ldb.Exec(`INSERT INTO indexed_files
		(id, rel_path, size_bytes, mod_time, content_hash, status, first_seen_at, last_seen_at, indexed_at)
		VALUES (?, ?, ?, ?, NULL, 'present', ?, ?, ?)`,
		id, relPath, size, now, now, now, now)
	return err
}

// seedLibraryFile inserts a file directly into the per-library database.
// doJSON2 is a helper that sends an authenticated request.
func (c *testClient) do(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf *bytes.Buffer
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		buf = bytes.NewBuffer(b)
	} else {
		buf = bytes.NewBuffer(nil)
	}
	req := httptest.NewRequest(method, path, buf)
	if body != nil {
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

// --- search tests ---

func TestHandleSearch_EmptyQuery(t *testing.T) {
	_, client, libID, _ := newSearchTestServer(t)

	rec := client.do(t, http.MethodGet,
		"/api/v1/libraries/"+libID+"/search", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var resp struct {
		Files []any `json:"files"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
}

func TestHandleSearch_Unauthenticated(t *testing.T) {
	h, _, libID, _ := newSearchTestServer(t)
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/libraries/"+libID+"/search", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestHandleSearch_LibraryNotFound(t *testing.T) {
	_, client, _, _ := newSearchTestServer(t)
	rec := client.do(t, http.MethodGet,
		"/api/v1/libraries/nonexistent/search", nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// --- tags tests ---

func TestHandleListTags_Empty(t *testing.T) {
	_, client, libID, _ := newSearchTestServer(t)

	rec := client.do(t, http.MethodGet, "/api/v1/libraries/"+libID+"/tags", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Tags []any `json:"tags"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// nil or empty slice both acceptable
}

func TestHandleCreateTag(t *testing.T) {
	_, client, libID, _ := newSearchTestServer(t)

	rec := client.do(t, http.MethodPost, "/api/v1/libraries/"+libID+"/tags",
		map[string]string{"name": "nature", "color": "#00ff00"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Tag struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			Color string `json:"color"`
		} `json:"tag"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Tag.ID == "" {
		t.Error("tag ID is empty")
	}
	if resp.Tag.Name != "nature" {
		t.Errorf("name = %q, want nature", resp.Tag.Name)
	}
}

func TestHandleCreateTag_MissingName(t *testing.T) {
	_, client, libID, _ := newSearchTestServer(t)

	rec := client.do(t, http.MethodPost, "/api/v1/libraries/"+libID+"/tags",
		map[string]string{"color": "#ff0000"})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestHandleCreateTag_Duplicate(t *testing.T) {
	_, client, libID, _ := newSearchTestServer(t)

	client.do(t, http.MethodPost, "/api/v1/libraries/"+libID+"/tags",
		map[string]string{"name": "dup"})
	rec := client.do(t, http.MethodPost, "/api/v1/libraries/"+libID+"/tags",
		map[string]string{"name": "dup"})
	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", rec.Code)
	}
}

func TestHandleDeleteTag(t *testing.T) {
	_, client, libID, _ := newSearchTestServer(t)

	create := client.do(t, http.MethodPost, "/api/v1/libraries/"+libID+"/tags",
		map[string]string{"name": "to-delete"})
	var cr struct {
		Tag struct {
			ID string `json:"id"`
		} `json:"tag"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &cr); err != nil {
		t.Fatalf("unmarshal create: %v", err)
	}

	rec := client.do(t, http.MethodDelete,
		"/api/v1/libraries/"+libID+"/tags/"+cr.Tag.ID, nil)
	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rec.Code)
	}
}

func TestHandleDeleteTag_NotFound(t *testing.T) {
	_, client, libID, _ := newSearchTestServer(t)

	rec := client.do(t, http.MethodDelete,
		"/api/v1/libraries/"+libID+"/tags/ghost", nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestHandleListTags_AfterCreate(t *testing.T) {
	_, client, libID, _ := newSearchTestServer(t)

	client.do(t, http.MethodPost, "/api/v1/libraries/"+libID+"/tags",
		map[string]string{"name": "beta"})
	client.do(t, http.MethodPost, "/api/v1/libraries/"+libID+"/tags",
		map[string]string{"name": "alpha"})

	rec := client.do(t, http.MethodGet, "/api/v1/libraries/"+libID+"/tags", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var resp struct {
		Tags []struct {
			Name string `json:"name"`
		} `json:"tags"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Tags) != 2 {
		t.Errorf("tag count = %d, want 2", len(resp.Tags))
	}
}

// --- albums tests ---

func TestHandleListAlbums_Empty(t *testing.T) {
	_, client, libID, _ := newSearchTestServer(t)

	rec := client.do(t, http.MethodGet, "/api/v1/libraries/"+libID+"/albums", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleCreateAlbum(t *testing.T) {
	_, client, libID, _ := newSearchTestServer(t)

	rec := client.do(t, http.MethodPost, "/api/v1/libraries/"+libID+"/albums",
		map[string]string{"name": "Summer Trip", "description": "2023 Italy"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Album struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"album"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Album.ID == "" {
		t.Error("album ID is empty")
	}
	if resp.Album.Name != "Summer Trip" {
		t.Errorf("name = %q, want Summer Trip", resp.Album.Name)
	}
	if resp.Album.Description != "2023 Italy" {
		t.Errorf("description = %q, want 2023 Italy", resp.Album.Description)
	}
}

func TestHandleCreateAlbum_MissingName(t *testing.T) {
	_, client, libID, _ := newSearchTestServer(t)

	rec := client.do(t, http.MethodPost, "/api/v1/libraries/"+libID+"/albums",
		map[string]string{"description": "no name"})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestHandleDeleteAlbum(t *testing.T) {
	_, client, libID, _ := newSearchTestServer(t)

	create := client.do(t, http.MethodPost, "/api/v1/libraries/"+libID+"/albums",
		map[string]string{"name": "to-delete"})
	var cr struct {
		Album struct {
			ID string `json:"id"`
		} `json:"album"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &cr); err != nil {
		t.Fatalf("unmarshal create: %v", err)
	}

	rec := client.do(t, http.MethodDelete,
		"/api/v1/libraries/"+libID+"/albums/"+cr.Album.ID, nil)
	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rec.Code)
	}
}

func TestHandleDeleteAlbum_NotFound(t *testing.T) {
	_, client, libID, _ := newSearchTestServer(t)

	rec := client.do(t, http.MethodDelete,
		"/api/v1/libraries/"+libID+"/albums/ghost", nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestHandleListAlbumFiles_Empty(t *testing.T) {
	_, client, libID, _ := newSearchTestServer(t)

	create := client.do(t, http.MethodPost, "/api/v1/libraries/"+libID+"/albums",
		map[string]string{"name": "empty"})
	var cr struct {
		Album struct {
			ID string `json:"id"`
		} `json:"album"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &cr); err != nil {
		t.Fatalf("unmarshal create: %v", err)
	}

	rec := client.do(t, http.MethodGet,
		"/api/v1/libraries/"+libID+"/albums/"+cr.Album.ID+"/files", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

// --- favorites tests ---

func TestHandleListFavorites_Empty(t *testing.T) {
	_, client, libID, _ := newSearchTestServer(t)

	rec := client.do(t, http.MethodGet, "/api/v1/libraries/"+libID+"/favorites", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Files []any `json:"files"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
}

func TestHandleAddFavorite_NotFound(t *testing.T) {
	_, client, libID, _ := newSearchTestServer(t)

	// Adding a favorite for a file that doesn't exist in indexed_files
	// will fail with a DB constraint or return an error based on the
	// ErrAlreadyFavorited path. Since the file doesn't exist at all,
	// the INSERT will fail with a foreign key constraint.
	rec := client.do(t, http.MethodPost,
		"/api/v1/libraries/"+libID+"/files/nonexistent/favorite", nil)
	// We expect some error (500 due to FK constraint or 409 due to already favorited).
	if rec.Code == http.StatusOK {
		t.Error("expected non-200 for favoriting nonexistent file")
	}
}

func TestHandleRemoveFavorite_NotFavorited(t *testing.T) {
	_, client, libID, _ := newSearchTestServer(t)

	// Try to remove a favorite that doesn't exist.
	rec := client.do(t, http.MethodDelete,
		"/api/v1/libraries/"+libID+"/files/ghost/favorite", nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestHandleSearch_InvalidQuery(t *testing.T) {
	_, client, libID, _ := newSearchTestServer(t)

	rec := client.do(t, http.MethodGet,
		"/api/v1/libraries/"+libID+"/search?q=%22unterminated", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	var resp struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Error.Code != "BAD_REQUEST" {
		t.Errorf("code = %q, want BAD_REQUEST", resp.Error.Code)
	}
}

func TestHandleSearch_TagFilter(t *testing.T) {
	_, client, libID, libRoot := newSearchTestServer(t)

	if err := seedLibraryFile(t, libRoot, "f1", "holiday/beach.jpg", 100); err != nil {
		t.Fatal(err)
	}
	if err := seedLibraryFile(t, libRoot, "f2", "work/report.pdf", 200); err != nil {
		t.Fatal(err)
	}

	var tagResp struct {
		Tag struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"tag"`
	}
	rec := client.do(t, http.MethodPost, "/api/v1/libraries/"+libID+"/tags",
		map[string]string{"name": "Holiday"})
	if err := json.Unmarshal(rec.Body.Bytes(), &tagResp); err != nil {
		t.Fatalf("unmarshal tag: %v", err)
	}

	rec = client.do(t, http.MethodPost,
		"/api/v1/libraries/"+libID+"/files/f1/tags",
		map[string]string{"tag_id": tagResp.Tag.ID})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("attach status = %d body=%s", rec.Code, rec.Body.String())
	}

	rec = client.do(t, http.MethodGet,
		"/api/v1/libraries/"+libID+"/search?tag=holiday", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Files []struct {
			ID string `json:"id"`
		} `json:"files"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Files) != 1 || resp.Files[0].ID != "f1" {
		t.Errorf("tag filter returned %+v, want [f1]", resp.Files)
	}
}

func TestHandleSearch_AlbumFilter(t *testing.T) {
	_, client, libID, libRoot := newSearchTestServer(t)

	if err := seedLibraryFile(t, libRoot, "f1", "a.jpg", 100); err != nil {
		t.Fatal(err)
	}
	if err := seedLibraryFile(t, libRoot, "f2", "b.jpg", 200); err != nil {
		t.Fatal(err)
	}

	var albumResp struct {
		Album struct {
			ID string `json:"id"`
		} `json:"album"`
	}
	rec := client.do(t, http.MethodPost, "/api/v1/libraries/"+libID+"/albums",
		map[string]string{"name": "Summer"})
	if err := json.Unmarshal(rec.Body.Bytes(), &albumResp); err != nil {
		t.Fatalf("unmarshal album: %v", err)
	}

	rec = client.do(t, http.MethodPost,
		"/api/v1/libraries/"+libID+"/albums/"+albumResp.Album.ID+"/files/f1", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("add to album status = %d body=%s", rec.Code, rec.Body.String())
	}

	rec = client.do(t, http.MethodGet,
		"/api/v1/libraries/"+libID+"/search?album="+albumResp.Album.ID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Files []struct {
			ID string `json:"id"`
		} `json:"files"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Files) != 1 || resp.Files[0].ID != "f1" {
		t.Errorf("album filter returned %+v, want [f1]", resp.Files)
	}
}

func TestHandleSearch_SizeFilter(t *testing.T) {
	_, client, libID, libRoot := newSearchTestServer(t)

	if err := seedLibraryFile(t, libRoot, "small", "small.jpg", 1000); err != nil {
		t.Fatal(err)
	}
	if err := seedLibraryFile(t, libRoot, "big", "big.jpg", 9000); err != nil {
		t.Fatal(err)
	}

	rec := client.do(t, http.MethodGet,
		"/api/v1/libraries/"+libID+"/search?min_size=5000", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Files []struct {
			ID string `json:"id"`
		} `json:"files"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Files) != 1 || resp.Files[0].ID != "big" {
		t.Errorf("min_size filter returned %+v, want [big]", resp.Files)
	}

	rec = client.do(t, http.MethodGet,
		"/api/v1/libraries/"+libID+"/search?min_size=10&max_size=5&q=beach", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("min>max status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestHandleSearchUnauthenticated(t *testing.T) {
	h, _, libID, _ := newSearchTestServer(t)
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/libraries/"+libID+"/tags", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("tags unauthenticated status = %d, want 401", rec.Code)
	}
}

func TestHandleAlbumsUnauthenticated(t *testing.T) {
	h, _, libID, _ := newSearchTestServer(t)
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/libraries/"+libID+"/albums", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("albums unauthenticated status = %d, want 401", rec.Code)
	}
}

func TestHandleFavoritesUnauthenticated(t *testing.T) {
	h, _, libID, _ := newSearchTestServer(t)
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/libraries/"+libID+"/favorites", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("favorites unauthenticated status = %d, want 401", rec.Code)
	}
}
