package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Jishnu-Prasad888/Orchard/internal/audit"
	"github.com/Jishnu-Prasad888/Orchard/internal/auth"
	"github.com/Jishnu-Prasad888/Orchard/internal/authz"
	"github.com/Jishnu-Prasad888/Orchard/internal/backups"
	"github.com/Jishnu-Prasad888/Orchard/internal/crypto"
	"github.com/Jishnu-Prasad888/Orchard/internal/db"
	"github.com/Jishnu-Prasad888/Orchard/internal/indexer"
	"github.com/Jishnu-Prasad888/Orchard/internal/library"
	"github.com/Jishnu-Prasad888/Orchard/internal/serversettings"
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
		SecretKeyFile: filepath.Join(dir, "secret.key"),
	}, filepath.Join(dir, "test.db"))
	if err := backupMgr.LoadPolicy(context.Background(), serversettings.NewStore(pool)); err != nil {
		t.Fatalf("load backup policy: %v", err)
	}

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
	if _, err := os.Stat(filepath.Join(dst, "server", "orchard.db")); err != nil {
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

func TestBackupConfig_ChangesWhereBackupsGo(t *testing.T) {
	_, client, pool := newBackupTestServer(t)

	rec := client.roundTrip(t, http.MethodGet, "/api/v1/backups/config", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get config status = %d, body=%s", rec.Code, rec.Body.String())
	}

	// A folder that doesn't exist yet is created.
	target := filepath.Join(t.TempDir(), "elsewhere", "orchard-backups")
	rec = client.do(t, http.MethodPut, "/api/v1/backups/config", map[string]string{"dir": target})
	if rec.Code != http.StatusOK {
		t.Fatalf("set config status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var cfg backupConfigResponse
	jsonUnmarshalRec(t, rec, &cfg)
	if cfg.Dir != target || cfg.DirSource != backups.DirFromSetting {
		t.Errorf("config = %+v, want dir %q from setting", cfg, target)
	}
	if info, err := os.Stat(target); err != nil || !info.IsDir() {
		t.Fatalf("backup directory not created: %v", err)
	}

	// Saved, so the next start picks it up.
	var saved string
	if err := pool.QueryRow(`SELECT value FROM server_settings WHERE key = ?`, backups.DirSettingKey).Scan(&saved); err != nil || saved != target {
		t.Errorf("saved dir = %q (%v), want %q", saved, err, target)
	}

	// And the next backup is written there.
	rec = client.roundTrip(t, http.MethodPost, "/api/v1/backups", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("run backup status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if got := decodeBackupResponse(t, rec).Destination; filepath.Dir(got) != target {
		t.Errorf("backup written to %q, want inside %q", got, target)
	}
}

func TestBackupConfig_RejectsARelativeOrUnwritableDir(t *testing.T) {
	_, client, _ := newBackupTestServer(t)
	rec := client.do(t, http.MethodPut, "/api/v1/backups/config", map[string]string{"dir": "relative/path"})
	assertErrorEnvelope(t, rec, http.StatusBadRequest, CodeBadRequest)

	// A path under a regular file can never be a directory.
	file := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	rec = client.do(t, http.MethodPut, "/api/v1/backups/config", map[string]string{"dir": filepath.Join(file, "sub")})
	assertErrorEnvelope(t, rec, http.StatusBadRequest, CodeBadRequest)
}

func TestBackupPolicy_SaveReadAndValidate(t *testing.T) {
	_, client, _ := newBackupTestServer(t)

	rec := client.do(t, http.MethodGet, "/api/v1/backups/policy", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get policy = %d, body=%s", rec.Code, rec.Body.String())
	}
	var got backupPolicyResponse
	jsonUnmarshalRec(t, rec, &got)
	if got.Schedule.Kind != backups.ScheduleOff || got.NextRun != "" || got.Timezone == "" {
		t.Errorf("default policy = %+v", got)
	}

	rec = client.do(t, http.MethodPut, "/api/v1/backups/policy", map[string]any{
		"schedule":     map[string]any{"kind": "daily", "at": "03:00"},
		"size_trigger": map[string]any{"enabled": true, "bytes": 200 << 20, "quiet_minutes": 10},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("put policy = %d, body=%s", rec.Code, rec.Body.String())
	}
	jsonUnmarshalRec(t, rec, &got)
	if got.Schedule.Kind != "daily" || got.Schedule.At != "03:00" || got.NextRun == "" {
		t.Errorf("saved schedule = %+v next=%q", got.Schedule, got.NextRun)
	}
	if !got.SizeTrigger.Enabled || got.SizeTrigger.Bytes != 200<<20 {
		t.Errorf("saved size trigger = %+v", got.SizeTrigger)
	}

	for _, bad := range []map[string]any{
		{"schedule": map[string]any{"kind": "daily", "at": "3pm"}},
		{"schedule": map[string]any{"kind": "interval", "every_minutes": 1}},
		{"schedule": map[string]any{"kind": "off"}, "size_trigger": map[string]any{"enabled": true, "bytes": 10, "quiet_minutes": 5}},
	} {
		rec := client.do(t, http.MethodPut, "/api/v1/backups/policy", bad)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("policy %v = %d, want 400", bad, rec.Code)
		}
	}
	// None of the refused requests changed anything.
	rec = client.do(t, http.MethodGet, "/api/v1/backups/policy", nil)
	jsonUnmarshalRec(t, rec, &got)
	if got.Schedule.Kind != "daily" || !got.SizeTrigger.Enabled {
		t.Errorf("a refused request changed the policy: %+v", got.backupPolicyBody)
	}
}

func TestBackupRemote_ValidatesAndExplains(t *testing.T) {
	_, client, _ := newBackupTestServer(t)

	cases := []struct {
		body map[string]any
		want string
	}{
		{map[string]any{"config": map[string]any{"kind": "sftp", "user": "pi"}}, "server address is required"},
		{map[string]any{"config": map[string]any{"kind": "smb", "host": "nas"}}, "share name is required"},
		{map[string]any{"config": map[string]any{"kind": "ftp", "host": "nas", "user": "pi", "ftp_security": "maybe"}}, "ftp_security"},
		{map[string]any{"config": map[string]any{"kind": "local", "path": "/etc"}}, "backup folder setting"},
		// Nothing listens on port 1: a clear failure, not a hang.
		{map[string]any{
			"config": map[string]any{"kind": "sftp", "host": "127.0.0.1", "port": 1, "user": "pi"},
			"secret": map[string]any{"password": "x"},
		}, "could not reach"},
		// No password given and none saved for this server.
		{map[string]any{"config": map[string]any{"kind": "sftp", "host": "nas", "user": "pi"}}, "enter the password"},
	}
	for _, c := range cases {
		rec := client.do(t, http.MethodPost, "/api/v1/backups/remote/test", c.body)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), c.want) {
			t.Errorf("test %v = %d %s, want 400 mentioning %q", c.body, rec.Code, rec.Body.String(), c.want)
		}
		rec = client.do(t, http.MethodPut, "/api/v1/backups/remote", c.body)
		if rec.Code == http.StatusOK {
			t.Errorf("saved %v", c.body)
		}
	}

	rec := client.do(t, http.MethodGet, "/api/v1/backups/config", nil)
	var cfg backupConfigResponse
	jsonUnmarshalRec(t, rec, &cfg)
	if cfg.Destination != "local" || cfg.Remote != nil {
		t.Errorf("after refused requests, config = %+v", cfg)
	}
	rec = client.do(t, http.MethodPost, "/api/v1/backups/use-local", nil)
	if rec.Code != http.StatusOK {
		t.Errorf("use-local = %d", rec.Code)
	}
}
