package indexer

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/Jishnu-Prasad888/Cairn/internal/jobs"
	"github.com/Jishnu-Prasad888/Cairn/internal/librarydb"
)

func TestHasActiveIndexJobPreventsPileUp(t *testing.T) {
	cairnDir := filepath.Join(t.TempDir(), ".cairn")
	if err := os.MkdirAll(cairnDir, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := librarydb.OpenDB(cairnDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	q := jobs.NewQueue(db.DB(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := context.Background()

	if busy, err := hasActiveIndexJob(ctx, q); err != nil || busy {
		t.Fatalf("empty queue: busy=%v err=%v, want idle", busy, err)
	}
	// Other kinds of work do not block a rescan.
	if _, err := q.Enqueue(ctx, jobs.KindProcessMedia, nil); err != nil {
		t.Fatal(err)
	}
	if busy, _ := hasActiveIndexJob(ctx, q); busy {
		t.Fatal("a media job must not count as a running scan")
	}
	if _, err := q.Enqueue(ctx, jobs.KindIndex, nil); err != nil {
		t.Fatal(err)
	}
	if busy, err := hasActiveIndexJob(ctx, q); err != nil || !busy {
		t.Fatalf("queued scan: busy=%v err=%v, want busy", busy, err)
	}
}
