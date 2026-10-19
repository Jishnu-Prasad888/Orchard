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

	"github.com/Jishnu-Prasad888/Orchard/internal/audit"
	"github.com/Jishnu-Prasad888/Orchard/internal/auth"
	"github.com/Jishnu-Prasad888/Orchard/internal/authz"
	"github.com/Jishnu-Prasad888/Orchard/internal/crypto"
	"github.com/Jishnu-Prasad888/Orchard/internal/db"
	"github.com/Jishnu-Prasad888/Orchard/internal/indexer"
	"github.com/Jishnu-Prasad888/Orchard/internal/library"
)

// Clients poll the index status revision instead of re-fetching whole pages
// on a timer, so it must stay put while nothing happens and move on any edit,
// including ones that leave the file counts alone.
func TestIndexStatusRevisionMovesOnlyOnChange(t *testing.T) {
	tmpDir := t.TempDir()
	pool, err := db.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	if err := db.Migrate(pool); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	auditSvc := audit.New(pool, logger)
	libManager := library.NewManager(pool, logger, auditSvc, crypto.NewKeys(""))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	indexManager := indexer.NewIndexManager(logger)
	indexManager.SetBaseContext(ctx)
	handler := New(Dependencies{
		Logger: logger, DB: pool,
		Auth:      auth.NewService(pool, logger, auditSvc),
		Authz:     authz.NewService(pool, logger, nil),
		Libraries: libManager, Indexer: indexManager,
	}).Handler()
	admin := &testClient{handler: handler}
	admin.roundTrip(t, http.MethodPost, "/api/v1/auth/bootstrap",
		`{"username":"admin","password":"correct-horse-battery"}`)

	root := filepath.Join(tmpDir, "Books")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := admin.do(t, http.MethodPost, "/api/v1/libraries", map[string]string{"path": root, "name": "Books"})
	var created struct {
		Library struct{ ID string } `json:"library"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	libID := created.Library.ID

	revision := func() (rev string, scanned bool) {
		t.Helper()
		var body struct {
			Status struct {
				Revision  string    `json:"revision"`
				ActiveJob *struct{} `json:"active_job"`
				LastScan  *struct{} `json:"last_scan"`
			} `json:"status"`
		}
		rec := admin.do(t, http.MethodGet, "/api/v1/libraries/"+libID+"/index/status", nil)
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body.Status.Revision, body.Status.ActiveJob == nil && body.Status.LastScan != nil
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, done := revision(); done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("initial scan did not finish")
		}
		time.Sleep(20 * time.Millisecond)
	}

	first, _ := revision()
	if again, _ := revision(); again != first || first == "" {
		t.Fatalf("revision moved with nothing changing: %q → %q", first, again)
	}
	if rec := admin.do(t, http.MethodPost, "/api/v1/libraries/"+libID+"/collections",
		map[string]string{"name": "Favourites"}); rec.Code >= 400 {
		t.Fatalf("create collection = %d %s", rec.Code, rec.Body)
	}
	if after, _ := revision(); after == first {
		t.Fatalf("revision did not move after an edit: %q", after)
	}
}
