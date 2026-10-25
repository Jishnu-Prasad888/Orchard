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

	"github.com/Jishnu-Prasad888/Orchard/internal/indexer"
	"github.com/Jishnu-Prasad888/Orchard/internal/librarydb"
)

// --- helpers ---

func newTestDB(t *testing.T) *librarydb.DB {
	t.Helper()
	orchardDir := filepath.Join(t.TempDir(), ".orchard")
	if err := os.MkdirAll(orchardDir, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := librarydb.OpenDB(orchardDir)
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
		RelPath:     "photos/img001.pdf",
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

	got, err := store.GetByPath(ctx, "photos/img001.pdf")
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
	got, err := store.GetByPath(context.Background(), "does/not/exist.pdf")
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

	for _, path := range []string{"a.pdf", "b.pdf"} {
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
			ID: id, RelPath: id + ".pdf", SizeBytes: 1, ModTime: now,
			Status:      indexer.StatusPresent,
			FirstSeenAt: now, LastSeenAt: now, IndexedAt: now,
		}
		if err := store.Upsert(ctx, f); err != nil {
			t.Fatal(err)
		}
	}

	// "seen" only id-a and id-b; id-c should become missing.
	seenIDs := map[string]struct{}{"id-a": {}, "id-b": {}}
	newlyMissing, err := store.MarkMissing(ctx, seenIDs, now)
	if err != nil {
		t.Fatalf("MarkMissing: %v", err)
	}
	if len(newlyMissing) != 1 || newlyMissing[0].RelPath != "id-c.pdf" {
		t.Errorf("MarkMissing = %+v, want only id-c.pdf", newlyMissing)
	}

	f, _ := store.GetByPath(ctx, "id-c.pdf")
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
			ID: id, RelPath: id + ".pdf", SizeBytes: 1, ModTime: now,
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
	orchardDir := filepath.Join(root, ".orchard")
	if err := os.MkdirAll(orchardDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "a.pdf"), "photo a")
	writeFile(t, filepath.Join(root, "b.epub"), "notes b")

	mgr := indexer.NewIndexManager(discardLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
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
	if status.LastScan == nil {
		t.Fatal("LastScan is nil, want the finished scan's summary")
	}
	if status.LastScan.TotalFiles != 2 || status.LastScan.NewFiles != 2 {
		t.Errorf("LastScan = %+v, want total=2 new=2", status.LastScan)
	}
}

func TestIndexManagerWorkersRestartIsIdempotent(t *testing.T) {
	root := t.TempDir()
	orchardDir := filepath.Join(root, ".orchard")
	if err := os.MkdirAll(orchardDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "a.pdf"), "photo a")

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
	writeFile(t, filepath.Join(root, "img001.pdf"), "photo data")
	writeFile(t, filepath.Join(root, "2022", "img002.pdf"), "another photo")

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

func TestScannerSkipsOrchardDir(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "real.pdf"), "photo")
	writeFile(t, filepath.Join(root, ".orchard", "library.json"), `{"id":"x"}`)
	writeFile(t, filepath.Join(root, ".orchard", "library.db"), "db content")

	db := newTestDB(t)
	scanner := indexer.NewScanner(indexer.NewStateStore(db.DB()), discardLogger())
	result, err := scanner.Scan(context.Background(), "lib1", root)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if result.TotalFiles != 1 {
		t.Errorf("total = %d, want 1 (.orchard files must not be indexed)", result.TotalFiles)
	}
}

func TestScannerUnchangedFiles(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "photo.pdf"), "stable content")

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
	path := filepath.Join(root, "photo.pdf")
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
	path := filepath.Join(root, "photo.pdf")
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
	if len(r2.MissingPaths) != 1 || r2.MissingPaths[0] != "photo.pdf" {
		t.Errorf("MissingPaths = %v, want [photo.pdf]", r2.MissingPaths)
	}

	// Metadata must still be in the index as 'missing'.
	f, err := store.GetByPath(context.Background(), "photo.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if f == nil || f.Status != indexer.StatusMissing {
		t.Errorf("status = %v, want missing", f)
	}
}

func TestScannerRecordsUnreadableFileAsError(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "locked.pdf")
	writeFile(t, path, "some content")
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) }) // so TempDir cleanup can remove it

	if os.Geteuid() == 0 {
		t.Skip("running as root: file permissions do not block reads")
	}

	db := newTestDB(t)
	store := indexer.NewStateStore(db.DB())
	scanner := indexer.NewScanner(store, discardLogger())

	result, err := scanner.Scan(context.Background(), "lib1", root)
	if err != nil {
		t.Fatal(err)
	}
	if result.Errors != 1 {
		t.Errorf("Errors = %d, want 1", result.Errors)
	}
	if len(result.ErrorDetails) != 1 || result.ErrorDetails[0].Path != "locked.pdf" {
		t.Errorf("ErrorDetails = %+v, want one entry for locked.pdf", result.ErrorDetails)
	}
	if result.ErrorDetails[0].Reason == "" {
		t.Error("ErrorDetails[0].Reason is empty, want a reason")
	}
}

func TestScannerMovedFile(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "2022", "photo.pdf")
	dst := filepath.Join(root, "archive", "photo.pdf")
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
	old, _ := store.GetByPath(context.Background(), "2022/photo.pdf")
	if old != nil && old.Status == indexer.StatusPresent {
		t.Error("old path still marked present after move")
	}

	// New path should be present.
	newF, _ := store.GetByPath(context.Background(), "archive/photo.pdf")
	if newF == nil || newF.Status != indexer.StatusPresent {
		t.Errorf("new path status = %v, want present", newF)
	}
}

func TestScannerDoesNotModifyOriginalFiles(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "video.epub")
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
	writeFile(t, filepath.Join(root, "a.pdf"), "stable a")
	writeFile(t, filepath.Join(root, "b.pdf"), "stable b")

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
	writeFile(t, filepath.Join(root, "c.pdf"), "new file c")

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

// A rescan of an unchanged library must not write: on a Pi's SD card,
// rewriting every row every minute is constant wear for nothing. A file that
// returns after going missing is the exception.
func TestScannerRescanWritesNothingUnlessAFileReturns(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.pdf"), "a")
	writeFile(t, filepath.Join(root, "b.pdf"), "b")

	db := newTestDB(t)
	store := indexer.NewStateStore(db.DB())
	scanner := indexer.NewScanner(store, discardLogger())
	ctx := context.Background()
	if _, err := scanner.Scan(ctx, "lib1", root); err != nil {
		t.Fatal(err)
	}
	changes := func() int64 {
		var n int64
		if err := db.DB().QueryRow(`SELECT total_changes()`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	before := changes()
	result, err := scanner.Scan(ctx, "lib1", root)
	if err != nil {
		t.Fatal(err)
	}
	if result.Unchanged != 2 || len(result.Changes) != 0 {
		t.Fatalf("rescan = %+v", result)
	}
	if after := changes(); after != before {
		t.Fatalf("unchanged rescan wrote %d rows", after-before)
	}

	// b goes missing, then comes back: it must be present again.
	moved := filepath.Join(t.TempDir(), "b.pdf")
	if err := os.Rename(filepath.Join(root, "b.pdf"), moved); err != nil {
		t.Fatal(err)
	}
	if r, _ := scanner.Scan(ctx, "lib1", root); r.Missing != 1 {
		t.Fatalf("missing = %d, want 1", r.Missing)
	}
	if err := os.Rename(moved, filepath.Join(root, "b.pdf")); err != nil {
		t.Fatal(err)
	}
	if _, err := scanner.Scan(ctx, "lib1", root); err != nil {
		t.Fatal(err)
	}
	f, err := store.GetByPath(ctx, "b.pdf")
	if err != nil || f == nil || f.Status != indexer.StatusPresent {
		t.Fatalf("returned file = %+v, %v", f, err)
	}
}

// A scanned file must carry its media type at once, not only after the
// database is next reopened (the Photos page filters on it).
func TestScannerClassifiesMediaTypeWithoutAReopen(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.epub"), "a")
	writeFile(t, filepath.Join(root, "b.pdf"), "b")
	db := newTestDB(t)
	scanner := indexer.NewScanner(indexer.NewStateStore(db.DB()), discardLogger())
	if _, err := scanner.Scan(context.Background(), "lib1", root); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{"a.epub": "document", "b.pdf": "document"} {
		var got string
		if err := db.DB().QueryRow(`SELECT media_type FROM indexed_files WHERE rel_path = ?`, path).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("%s media_type = %q, want %q", path, got, want)
		}
	}
}

// Only books are indexed: an AppImage, an archive or a photo beside them is
// left alone, and the scan says how many it passed over.
func TestScannerIndexesOnlyBooks(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "Novel.EPUB"), "epub")
	writeFile(t, filepath.Join(root, "papers", "paper.pdf"), "pdf")
	writeFile(t, filepath.Join(root, "Tool-x86_64.AppImage"), "elf")
	writeFile(t, filepath.Join(root, "papers", "data.zip"), "zip")
	writeFile(t, filepath.Join(root, "cover.jpg"), "jpg")
	writeFile(t, filepath.Join(root, "notes.md"), "md")

	db := newTestDB(t)
	scanner := indexer.NewScanner(indexer.NewStateStore(db.DB()), discardLogger())
	result, err := scanner.Scan(context.Background(), "lib1", root)
	if err != nil {
		t.Fatal(err)
	}
	if result.NewFiles != 2 || result.TotalFiles != 2 || result.Skipped != 4 {
		t.Errorf("new = %d, total = %d, skipped = %d; want 2, 2, 4",
			result.NewFiles, result.TotalFiles, result.Skipped)
	}
	var n int
	if err := db.DB().QueryRow(`SELECT COUNT(*) FROM indexed_files`).Scan(&n); err != nil || n != 2 {
		t.Errorf("indexed rows = %d (%v), want 2", n, err)
	}
}

// Rows for non-books left by an older index are removed on the next scan,
// not reported as missing: the files are still there, just not listed.
func TestScannerRemovesNonBooksFromAnOlderIndex(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "book.pdf"), "pdf")
	writeFile(t, filepath.Join(root, "Tool.AppImage"), "elf")

	db := newTestDB(t)
	store := indexer.NewStateStore(db.DB())
	now := time.Now().UTC()
	for _, f := range []*indexer.IndexedFile{
		{ID: "app", RelPath: "Tool.AppImage", SizeBytes: 3, ModTime: now, Status: indexer.StatusPresent,
			FirstSeenAt: now, LastSeenAt: now, IndexedAt: now},
		{ID: "gone", RelPath: "old/setup.exe", SizeBytes: 3, ModTime: now, Status: indexer.StatusMissing,
			FirstSeenAt: now, LastSeenAt: now, IndexedAt: now},
	} {
		if err := store.Upsert(context.Background(), f); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.DB().Exec(
		`INSERT INTO favorites (user_id, file_id, created_at) VALUES ('u1', 'app', ?)`,
		now.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}

	scanner := indexer.NewScanner(store, discardLogger())
	result, err := scanner.Scan(context.Background(), "lib1", root)
	if err != nil {
		t.Fatal(err)
	}
	if result.Removed != 2 || result.Missing != 0 || result.NewFiles != 1 {
		t.Errorf("removed = %d, missing = %d, new = %d; want 2, 0, 1",
			result.Removed, result.Missing, result.NewFiles)
	}
	var rows, favs int
	_ = db.DB().QueryRow(`SELECT COUNT(*) FROM indexed_files`).Scan(&rows)
	_ = db.DB().QueryRow(`SELECT COUNT(*) FROM favorites`).Scan(&favs)
	if rows != 1 || favs != 0 {
		t.Errorf("rows = %d, favorites = %d; want only the book left", rows, favs)
	}
	if _, err := os.Stat(filepath.Join(root, "Tool.AppImage")); err != nil {
		t.Errorf("the AppImage itself must stay on disk: %v", err)
	}
}
