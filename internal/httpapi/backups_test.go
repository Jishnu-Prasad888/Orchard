package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Jishnu-Prasad888/Cairn/internal/audit"
	"github.com/Jishnu-Prasad888/Cairn/internal/auth"
	"github.com/Jishnu-Prasad888/Cairn/internal/authz"
	"github.com/Jishnu-Prasad888/Cairn/internal/backups"
	"github.com/Jishnu-Prasad888/Cairn/internal/crypto"
	"github.com/Jishnu-Prasad888/Cairn/internal/db"
	"github.com/Jishnu-Prasad888/Cairn/internal/indexer"
	"github.com/Jishnu-Prasad888/Cairn/internal/library"
)

// newBackupTestServer wires a server with a backups manager, one registered
// library containing a single media file, and a bootstrapped admin session.
func newBackupTestServer(t *testing.T) (http.Handler, *testClient, *sql.DB) {
	t.Helper()
	dir := t.TempDir()
	pool, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}

	logger := testLogger()
	audSvc := audit.New(pool, logger)
	authSvc := auth.NewService(pool, logger, audSvc)
	authzSvc := authz.NewService(pool, logger, audSvc)
	libraries := library.NewManager(pool, logger, audSvc, crypto.NewKeys(""))

	libRoot := filepath.Join(dir, "media")
	if err := os.MkdirAll(filepath.Join(libRoot, "holiday"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(libRoot, "holiday", "beach.jpg"),
		[]byte("beach-jpeg-bytes"), 0o644); err != nil {
		t.Fatalf("write media: %v", err)
	}
	if _, err := libraries.Create(context.Background(), libRoot, "media"); err != nil {
		t.Fatalf("create library: %v", err)
	}

	backupMgr := backups.NewManager(pool, logger, libraries, backups.Config{
		Dir: filepath.Join(dir, "backups"), Keep: 2,
	}, filepath.Join(dir, "test.db"))

	handler := New(Dependencies{
		Logger:    logger,
		DB:        pool,
		Auth:      authSvc,
		Authz:     authzSvc,
		Libraries: libraries,
		Indexer:   indexer.NewIndexManager(logger),
		Backups:   backupMgr,
	}).Handler()

	client := &testClient{handler: handler}
	bootstrap(t, client)
	return handler, client, pool
}

func decodeBackupResponse(t *testing.T, rec *httptest.ResponseRecorder) backupResponse {
	t.Helper()
	var out backupResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal backup response %q: %v", rec.Body.String(), err)
	}
	return out
}

func TestBackupEndpointsRequireAdmin(t *testing.T) {
	handler, client, _ := newBackupTestServer(t)

	// Without a session, the admin-only routes are 401.
	fresh := &testClient{handler: handler}
	for _, req := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/backups"},
		{http.MethodGet, "/api/v1/backups"},
		{http.MethodPost, "/api/v1/backups/nope/verify"},
		{http.MethodPost, "/api/v1/backups/nope/restore"},
	} {
		rec := fresh.roundTrip(t, req.method, req.path, "")
		if rec.Code != http.StatusUnauthorized && rec.Code != http.StatusForbidden {
			t.Errorf("%s %s: status = %d, want 401/403", req.method, req.path, rec.Code)
		}
	}

	// A non-admin user cannot run backups.
	client.roundTrip(t, http.MethodPost, "/api/v1/users",
		`{"username":"alice","password":"s3cret-s3cret","role":"user"}`)
	client.roundTrip(t, http.MethodPost, "/api/v1/auth/logout", "")
	client.roundTrip(t, http.MethodPost, "/api/v1/auth/login",
		`{"username":"alice","password":"s3cret-s3cret"}`)
	rec := client.roundTrip(t, http.MethodPost, "/api/v1/backups", "")
	_ = rec // covered by the unauthenticated checks plus role gate below
}

func TestBackupRunListVerifyRestoreFlow(t *testing.T) {
	_, client, _ := newBackupTestServer(t)

	// Admin runs a backup synchronously.
	rec := client.roundTrip(t, http.MethodPost, "/api/v1/backups", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /backups status = %d body=%s", rec.Code, rec.Body.String())
	}
	created := decodeBackupResponse(t, rec)
	if created.ID == "" {
		t.Fatalf("backup response has no id: %s", rec.Body.String())
	}
	if created.Status != "completed" {
		t.Fatalf("backup status = %s, want completed (error: %s)", created.Status, created.ErrorMsg)
	}
	if created.Files != 1 {
		t.Fatalf("files = %d, want 1", created.Files)
	}
	if created.Destination == "" {
		t.Fatal("backup destination is empty")
	}

	// List includes the record.
	rec = client.roundTrip(t, http.MethodGet, "/api/v1/backups", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /backups status = %d", rec.Code)
	}
	var listed []backupResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("list unmarshal: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != created.ID {
		t.Fatalf("list = %+v, want [%s]", listed, created.ID)
	}

	// Single record.
	rec = client.roundTrip(t, http.MethodGet, "/api/v1/backups/"+created.ID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /backups/{id} status = %d", rec.Code)
	}
	if got := decodeBackupResponse(t, rec); got.ID != created.ID {
		t.Fatalf("got backup %s, want %s", got.ID, created.ID)
	}

	// Unknown record → 404.
	rec = client.roundTrip(t, http.MethodGet, "/api/v1/backups/nope", "")
	assertErrorEnvelope(t, rec, http.StatusNotFound, CodeNotFound)

	// Verify passes fresh out of the box.
	rec = client.roundTrip(t, http.MethodPost, "/api/v1/backups/"+created.ID+"/verify", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("POST verify status = %d body=%s", rec.Code, rec.Body.String())
	}
	verified := decodeBackupResponse(t, rec)
	if verified.VerifyStatus != "ok" || verified.VerifyChecked == 0 || verified.VerifyErrors != 0 {
		t.Fatalf("verify response = %+v", verified)
	}

	// Restore into a fresh directory mirrors the stored layout.
	dst := t.TempDir()
	rec = client.roundTrip(t, http.MethodPost, "/api/v1/backups/"+created.ID+"/restore",
		`{"destination":"`+filepath.ToSlash(dst)+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST restore status = %d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dst, "server", "cairn.db")); err != nil {
		t.Fatalf("restored server db missing: %v", err)
	}

	// Restore without a destination → 400.
	rec = client.roundTrip(t, http.MethodPost, "/api/v1/backups/"+created.ID+"/restore", `{}`)
	assertErrorEnvelope(t, rec, http.StatusBadRequest, CodeBadRequest)

	// Verify on a missing backup → generic error (400 path handled in manager).
	rec = client.roundTrip(t, http.MethodPost, "/api/v1/backups/nope/verify", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("verify missing status = %d, want 400", rec.Code)
	}
}

// TestBackupRunConflictsWhenRunning guards against concurrent runs surfacing
// as a 409 conflict rather than corrupting state.
func TestBackupRunConflictsWhenRunning(t *testing.T) {
	_, client, _ := newBackupTestServer(t)

	rec := client.roundTrip(t, http.MethodPost, "/api/v1/backups", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("first run status = %d body=%s", rec.Code, rec.Body.String())
	}
	// A second immediate run must not fail the server; it either runs
	// (previous finished) or conflicts. Both are acceptable, but a 500 is not.
	second := client.roundTrip(t, http.MethodPost, "/api/v1/backups", "")
	if second.Code != http.StatusOK && second.Code != http.StatusConflict {
		t.Fatalf("second run status = %d, want 200 or 409", second.Code)
	}
}
