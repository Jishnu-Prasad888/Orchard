package indexer_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Jishnu-Prasad888/Cairn/internal/crypto"
	"github.com/Jishnu-Prasad888/Cairn/internal/indexer"
	"github.com/Jishnu-Prasad888/Cairn/internal/librarydb"
	"github.com/Jishnu-Prasad888/Cairn/internal/metadata"
)

// --- helpers ---

func newTestDB(t *testing.T) *librarydb.DB {
	t.Helper()
	cairnDir := filepath.Join(t.TempDir(), ".cairn")
	if err := os.MkdirAll(cairnDir, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := librarydb.OpenDB(cairnDir)
	if err != nil {
		t.Fatalf("open library db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fileHash(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// --- StateStore tests ---

func TestStateStoreUpsertAndGet(t *testing.T) {
	db := newTestDB(t)
	store := indexer.NewStateStore(db.DB())
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	f := &indexer.IndexedFile{
		ID:          "test-id-1",
		RelPath:     "photos/img001.jpg",
		SizeBytes:   1024,
		ModTime:     now,
		ContentHash: "abc123",
		Status:      indexer.StatusPresent,
		FirstSeenAt: now,
		LastSeenAt:  now,
		IndexedAt:   now,
	}
	if err := store.Upsert(ctx, f); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := store.GetByPath(ctx, "photos/img001.jpg")
	if err != nil {
		t.Fatalf("GetByPath: %v", err)
	}
	if got == nil {
		t.Fatal("GetByPath returned nil")
	}
	if got.ID != f.ID || got.SizeBytes != f.SizeBytes || got.ContentHash != f.ContentHash {
		t.Errorf("got = %+v, want %+v", got, f)
	}
}

func TestStateStoreGetByPathMissing(t *testing.T) {
	db := newTestDB(t)
	store := indexer.NewStateStore(db.DB())
	got, err := store.GetByPath(context.Background(), "does/not/exist.jpg")
	if err != nil {
		t.Fatalf("GetByPath: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestStateStoreGetByHash(t *testing.T) {
	db := newTestDB(t)
	store := indexer.NewStateStore(db.DB())
	ctx := context.Background()
	now := time.Now().UTC()

	for _, path := range []string{"a.jpg", "b.jpg"} {
		f := &indexer.IndexedFile{
			ID: path, RelPath: path, SizeBytes: 100, ModTime: now,
			ContentHash: "shared-hash", Status: indexer.StatusPresent,
			FirstSeenAt: now, LastSeenAt: now, IndexedAt: now,
		}
		if err := store.Upsert(ctx, f); err != nil {
			t.Fatal(err)
		}
	}

	files, err := store.GetByHash(ctx, "shared-hash")
	if err != nil {
		t.Fatalf("GetByHash: %v", err)
	}
	if len(files) != 2 {
		t.Errorf("len = %d, want 2", len(files))
	}
}

func TestStateStoreMarkMissing(t *testing.T) {
	db := newTestDB(t)
	store := indexer.NewStateStore(db.DB())
	ctx := context.Background()
	now := time.Now().UTC()

	ids := []string{"id-a", "id-b", "id-c"}
	for _, id := range ids {
		f := &indexer.IndexedFile{
			ID: id, RelPath: id + ".jpg", SizeBytes: 1, ModTime: now,
			Status:      indexer.StatusPresent,
			FirstSeenAt: now, LastSeenAt: now, IndexedAt: now,
		}
		if err := store.Upsert(ctx, f); err != nil {
			t.Fatal(err)
		}
	}

	// "seen" only id-a and id-b; id-c should become missing.
	seenIDs := map[string]struct{}{"id-a": {}, "id-b": {}}
	n, err := store.MarkMissing(ctx, seenIDs, now)
	if err != nil {
		t.Fatalf("MarkMissing: %v", err)
	}
	if n != 1 {
		t.Errorf("MarkMissing count = %d, want 1", n)
	}

	f, _ := store.GetByPath(ctx, "id-c.jpg")
	if f == nil || f.Status != indexer.StatusMissing {
		t.Errorf("id-c status = %v, want missing", f)
	}

	present, missing, _, err := store.Counts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if present != 2 || missing != 1 {
		t.Errorf("present=%d missing=%d, want 2/1", present, missing)
	}
}

func TestStateStoreCounts(t *testing.T) {
	db := newTestDB(t)
	store := indexer.NewStateStore(db.DB())
	ctx := context.Background()
	now := time.Now().UTC()

	statuses := []indexer.Status{
		indexer.StatusPresent, indexer.StatusPresent,
		indexer.StatusMissing,
		indexer.StatusDeleted,
	}
	for i, s := range statuses {
		id := string(rune('a' + i))
		f := &indexer.IndexedFile{
			ID: id, RelPath: id + ".jpg", SizeBytes: 1, ModTime: now,
			Status:      s,
			FirstSeenAt: now, LastSeenAt: now, IndexedAt: now,
		}
		if err := store.Upsert(ctx, f); err != nil {
			t.Fatal(err)
		}
	}

	present, missing, deleted, err := store.Counts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if present != 2 || missing != 1 || deleted != 1 {
		t.Errorf("counts = %d/%d/%d, want 2/1/1", present, missing, deleted)
	}
}

// TestIndexManagerWorkerExecutesScanJob guards against the regression where
// scan jobs were enqueued into the per-library queue but no worker ever ran
// them: a freshly triggered scan must actually index the library.
func TestIndexManagerWorkerExecutesScanJob(t *testing.T) {
	root := t.TempDir()
	cairnDir := filepath.Join(root, ".cairn")
	if err := os.MkdirAll(cairnDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "a.jpg"), "photo a")
	writeFile(t, filepath.Join(root, "b.txt"), "notes b")

	mgr := indexer.NewIndexManager(discardLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr.SetMediaProcessor(metadata.NewProcessor(discardLogger(), crypto.NewKeys("")))
	if err := mgr.StartLibraryWorker(ctx, "lib1", root); err != nil {
		t.Fatalf("start library worker: %v", err)
	}
	defer mgr.StopLibraryWorker("lib1")

	if _, err := mgr.TriggerScan(ctx, "lib1", root); err != nil {
		t.Fatalf("trigger scan: %v", err)
	}

	// The worker claims one job per poll tick, so allow the scan job plus its
	// process_media follow-ups to drain.
	deadline := time.Now().Add(30 * time.Second)
	var status *indexer.IndexStatus
	for time.Now().Before(deadline) {
		st, err := mgr.Status(ctx, "lib1", root)
		if err != nil {
			t.Fatalf("index status: %v", err)
		}
		if st.Present == 2 && st.Missing == 0 && st.ActiveJob == nil {
			status = st
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if status == nil {
		t.Fatal("index scan job was never executed by the library worker")
	}
}

func TestIndexManagerWorkersRestartIsIdempotent(t *testing.T) {
	root := t.TempDir()
	cairnDir := filepath.Join(root, ".cairn")
	if err := os.MkdirAll(cairnDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "a.jpg"), "photo a")

	mgr := indexer.NewIndexManager(discardLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Idempotent: starting the same library twice must not double-start a
	// worker. The second call simply no-ops.
	for i := 0; i < 2; i++ {
		if err := mgr.StartLibraryWorker(ctx, "lib1", root); err != nil {
			t.Fatalf("start worker (call %d): %v", i, err)
		}
	}
	if _, err := mgr.TriggerScan(ctx, "lib1", root); err != nil {
		t.Fatalf("trigger scan: %v", err)
	}

	// StopLibraryWorker must not resurrect a stopped worker.
	mgr.StopLibraryWorker("lib1")

	deadline := time.Now().Add(5 * time.Second)
	var status *indexer.IndexStatus
	for time.Now().Before(deadline) {
		st, err := mgr.Status(ctx, "lib1", root)
		if err != nil {
			t.Fatalf("index status: %v", err)
		}
		status = st
		time.Sleep(100 * time.Millisecond)
	}
	if status != nil && status.Present == 1 {
		t.Fatalf("job ran after worker was stopped: present=%d", status.Present)
	}
}

// --- Scanner tests ---

func TestScannerNewFiles(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "img001.jpg"), "photo data")
	writeFile(t, filepath.Join(root, "2022", "img002.jpg"), "another photo")

	db := newTestDB(t)
	store := indexer.NewStateStore(db.DB())
	scanner := indexer.NewScanner(store, discardLogger())

	result, err := scanner.Scan(context.Background(), "lib1", root)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if result.TotalFiles != 2 {
		t.Errorf("total = %d, want 2", result.TotalFiles)
	}
	if result.NewFiles != 2 {
		t.Errorf("new = %d, want 2", result.NewFiles)
	}
	if result.Missing != 0 {
		t.Errorf("missing = %d, want 0", result.Missing)
	}
}

func TestScannerSkipsCairnDir(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "real.jpg"), "photo")
	writeFile(t, filepath.Join(root, ".cairn", "library.json"), `{"id":"x"}`)
	writeFile(t, filepath.Join(root, ".cairn", "library.db"), "db content")

	db := newTestDB(t)
	scanner := indexer.NewScanner(indexer.NewStateStore(db.DB()), discardLogger())
	result, err := scanner.Scan(context.Background(), "lib1", root)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if result.TotalFiles != 1 {
		t.Errorf("total = %d, want 1 (.cairn files must not be indexed)", result.TotalFiles)
	}
}

func TestScannerUnchangedFiles(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "photo.jpg"), "stable content")

	db := newTestDB(t)
	store := indexer.NewStateStore(db.DB())
	scanner := indexer.NewScanner(store, discardLogger())

	// First scan: new file.
	r1, err := scanner.Scan(context.Background(), "lib1", root)
	if err != nil {
		t.Fatalf("scan 1: %v", err)
	}
	if r1.NewFiles != 1 {
		t.Fatalf("scan1 new = %d, want 1", r1.NewFiles)
	}

	// Second scan without changes: unchanged.
	r2, err := scanner.Scan(context.Background(), "lib1", root)
	if err != nil {
		t.Fatalf("scan 2: %v", err)
	}
	if r2.Unchanged != 1 || r2.NewFiles != 0 {
		t.Errorf("scan2: unchanged=%d new=%d, want 1/0", r2.Unchanged, r2.NewFiles)
	}
}

func TestScannerModifiedFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "photo.jpg")
	writeFile(t, path, "original content")

	db := newTestDB(t)
	store := indexer.NewStateStore(db.DB())
	scanner := indexer.NewScanner(store, discardLogger())

	if _, err := scanner.Scan(context.Background(), "lib1", root); err != nil {
		t.Fatal(err)
	}

	// Modify the file: change content and bump mtime.
	time.Sleep(10 * time.Millisecond)
	writeFile(t, path, "modified content")
	// Force mtime change (WriteFile usually does this, but make explicit).
	now := time.Now().Add(time.Second)
	if err := os.Chtimes(path, now, now); err != nil {
		t.Fatal(err)
	}

	r2, err := scanner.Scan(context.Background(), "lib1", root)
	if err != nil {
		t.Fatalf("scan 2: %v", err)
	}
	if r2.Modified != 1 {
		t.Errorf("modified = %d, want 1", r2.Modified)
	}
}

func TestScannerMissingFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "photo.jpg")
	writeFile(t, path, "some content")

	db := newTestDB(t)
	store := indexer.NewStateStore(db.DB())
	scanner := indexer.NewScanner(store, discardLogger())

	if _, err := scanner.Scan(context.Background(), "lib1", root); err != nil {
		t.Fatal(err)
	}

	// Remove the file.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	r2, err := scanner.Scan(context.Background(), "lib1", root)
	if err != nil {
		t.Fatalf("scan 2: %v", err)
	}
	if r2.Missing != 1 {
		t.Errorf("missing = %d, want 1", r2.Missing)
	}

	// Metadata must still be in the index as 'missing'.
	f, err := store.GetByPath(context.Background(), "photo.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if f == nil || f.Status != indexer.StatusMissing {
		t.Errorf("status = %v, want missing", f)
	}
}

func TestScannerMovedFile(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "2022", "photo.jpg")
	dst := filepath.Join(root, "archive", "photo.jpg")
	writeFile(t, src, "unique photo content for move detection")

	db := newTestDB(t)
	store := indexer.NewStateStore(db.DB())
	scanner := indexer.NewScanner(store, discardLogger())

	// First scan: file appears at src.
	if _, err := scanner.Scan(context.Background(), "lib1", root); err != nil {
		t.Fatal(err)
	}

	// Move the file.
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(src, dst); err != nil {
		t.Fatal(err)
	}

	r2, err := scanner.Scan(context.Background(), "lib1", root)
	if err != nil {
		t.Fatalf("scan 2: %v", err)
	}
	if r2.Moved != 1 {
		t.Errorf("moved = %d, want 1", r2.Moved)
	}
	if r2.Missing != 0 {
		t.Errorf("missing = %d, want 0 (not separately counted when detected as moved)", r2.Missing)
	}

	// Old path should no longer be present.
	old, _ := store.GetByPath(context.Background(), "2022/photo.jpg")
	if old != nil && old.Status == indexer.StatusPresent {
		t.Error("old path still marked present after move")
	}

	// New path should be present.
	newF, _ := store.GetByPath(context.Background(), "archive/photo.jpg")
	if newF == nil || newF.Status != indexer.StatusPresent {
		t.Errorf("new path status = %v, want present", newF)
	}
}

func TestScannerDoesNotModifyOriginalFiles(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "video.mp4")
	content := "original video bytes that must never be touched"
	writeFile(t, path, content)

	// Record the original hash.
	originalHash := fileHash(t, path)
	originalStat, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	db := newTestDB(t)
	scanner := indexer.NewScanner(indexer.NewStateStore(db.DB()), discardLogger())

	// Run two scan passes.
	for i := 0; i < 2; i++ {
		if _, err := scanner.Scan(context.Background(), "lib1", root); err != nil {
			t.Fatalf("scan %d: %v", i+1, err)
		}
	}

	// Hash must be identical.
	afterHash := fileHash(t, path)
	if afterHash != originalHash {
		t.Errorf("file hash changed: before=%s after=%s", originalHash, afterHash)
	}

	// File size must not have changed.
	afterStat, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if afterStat.Size() != originalStat.Size() {
		t.Errorf("file size changed: before=%d after=%d", originalStat.Size(), afterStat.Size())
	}
}

func TestScannerReconnectedLibraryOnlyProcessesChanges(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.jpg"), "stable a")
	writeFile(t, filepath.Join(root, "b.jpg"), "stable b")

	db := newTestDB(t)
	store := indexer.NewStateStore(db.DB())
	scanner := indexer.NewScanner(store, discardLogger())

	// Initial scan.
	r1, err := scanner.Scan(context.Background(), "lib1", root)
	if err != nil {
		t.Fatal(err)
	}
	if r1.NewFiles != 2 {
		t.Fatalf("initial scan new = %d, want 2", r1.NewFiles)
	}

	// Add a new file; the existing ones are stable.
	writeFile(t, filepath.Join(root, "c.jpg"), "new file c")

	r2, err := scanner.Scan(context.Background(), "lib1", root)
	if err != nil {
		t.Fatal(err)
	}
	if r2.NewFiles != 1 {
		t.Errorf("incremental scan new = %d, want 1", r2.NewFiles)
	}
	if r2.Unchanged != 2 {
		t.Errorf("incremental scan unchanged = %d, want 2", r2.Unchanged)
	}
}
