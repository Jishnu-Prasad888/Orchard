package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Jishnu-Prasad888/Cairn/internal/audit"
	"github.com/Jishnu-Prasad888/Cairn/internal/auth"
	"github.com/Jishnu-Prasad888/Cairn/internal/authz"
	"github.com/Jishnu-Prasad888/Cairn/internal/crypto"
	"github.com/Jishnu-Prasad888/Cairn/internal/db"
	"github.com/Jishnu-Prasad888/Cairn/internal/indexer"
	"github.com/Jishnu-Prasad888/Cairn/internal/jobs"
	"github.com/Jishnu-Prasad888/Cairn/internal/library"
)

// Registering a library queues its first scan.
//
// A library is registered so its files can be seen, and a library that has
// never been scanned has no rows in indexed_files — so every page came up empty
// after adding one, and it stayed empty until an administrator found the
// Re-index button. The web client polls the index status and reloads, which
// only helps if something is actually running.
func TestCreateLibrary_QueuesInitialScan(t *testing.T) {
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
	indexManager := indexer.NewIndexManager(logger)

	handler := New(Dependencies{
		Logger:    logger,
		DB:        pool,
		Auth:      authSvc,
		Authz:     authzSvc,
		Libraries: libManager,
		Indexer:   indexManager,
	}).Handler()

	admin := &testClient{handler: handler}
	admin.roundTrip(t, http.MethodPost, "/api/v1/auth/bootstrap",
		`{"username":"admin","password":"correct-horse-battery"}`)

	root := filepath.Join(tmpDir, "Photos")
	if err := os.MkdirAll(filepath.Join(root, "2024"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "2024", "a.jpg"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := admin.do(t, http.MethodPost, "/api/v1/libraries",
		map[string]string{"path": root, "name": "Photos"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create library = %d, body=%s", rec.Code, rec.Body.String())
	}

	var created struct {
		Library struct {
			ID string `json:"id"`
		} `json:"library"`
		Indexing bool `json:"indexing"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal create library: %v", err)
	}
	if created.Library.ID == "" {
		t.Fatalf("create response has no library id: %s", rec.Body.String())
	}
	if !created.Indexing {
		t.Errorf("indexing = false, want true: the first scan was not queued")
	}

	// The job is observable through the endpoint the web client polls: it is
	// either still queued, already running, or finished — but never absent.
	// A library with no job at all is the bug this test exists for.
	rec = admin.do(t, http.MethodGet,
		"/api/v1/libraries/"+created.Library.ID+"/index/status", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("index status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var status struct {
		Status struct {
			ActiveJob *struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			} `json:"active_job"`
			LastJobID string `json:"last_job_id"`
		} `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatalf("unmarshal index status: %v", err)
	}
	jobID, jobState := status.Status.LastJobID, ""
	if status.Status.ActiveJob != nil {
		jobID, jobState = status.Status.ActiveJob.ID, status.Status.ActiveJob.Status
	}
	if jobID == "" {
		t.Fatalf("no index job for the new library: %s", rec.Body.String())
	}
	switch jobState {
	case jobs.StatusQueued, jobs.StatusRunning, jobs.StatusCompleted, "":
		// Any of these means a scan exists.
	default:
		t.Errorf("scan job is in state %q", jobState)
	}

	// And the scan actually runs to completion on its own, so the library is
	// usable without anyone clicking Re-index.
	deadline := time.Now().Add(10 * time.Second)
	for {
		rec := admin.do(t, http.MethodGet,
			"/api/v1/libraries/"+created.Library.ID+"/index/status", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("index status = %d, body=%s", rec.Code, rec.Body.String())
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
			t.Fatalf("unmarshal index status: %v", err)
		}
		if status.Status.ActiveJob == nil {
			if status.Status.LastJobID == "" {
				t.Fatalf("index job disappeared without completing")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("initial index scan did not finish: %s", rec.Body.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// A server built without an indexer (the case in a few embedders) still
// registers the library: refusing to register because a background worker is
// missing would lose the administrator's work.
func TestCreateLibrary_WithoutIndexerStillRegisters(t *testing.T) {
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
	handler := New(Dependencies{
		Logger:    logger,
		DB:        pool,
		Auth:      auth.NewService(pool, logger, auditSvc),
		Authz:     authz.NewService(pool, logger, nil),
		Libraries: library.NewManager(pool, logger, auditSvc, crypto.NewKeys("")),
	}).Handler()

	admin := &testClient{handler: handler}
	admin.roundTrip(t, http.MethodPost, "/api/v1/auth/bootstrap",
		`{"username":"admin","password":"correct-horse-battery"}`)

	root := filepath.Join(tmpDir, "Photos")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}

	rec := admin.do(t, http.MethodPost, "/api/v1/libraries",
		map[string]string{"path": root, "name": "Photos"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create library = %d, body=%s", rec.Code, rec.Body.String())
	}
	var created struct {
		Indexing bool `json:"indexing"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal create library: %v", err)
	}
	if created.Indexing {
		t.Errorf("indexing = true without an indexer configured")
	}

	// And it really is registered.
	lib, err := library.NewManager(pool, logger, auditSvc, crypto.NewKeys("")).
		List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(lib) != 1 {
		t.Errorf("library not registered: %v", lib)
	}
}
