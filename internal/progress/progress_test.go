package progress_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Jishnu-Prasad888/Orchard/internal/librarydb"
	"github.com/Jishnu-Prasad888/Orchard/internal/progress"
)

func newTestStore(t *testing.T) (*progress.Store, func(fileID, relPath string)) {
	t.Helper()
	orchardDir := filepath.Join(t.TempDir(), ".orchard")
	if err := os.MkdirAll(orchardDir, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := librarydb.Open(orchardDir)
	if err != nil {
		t.Fatalf("open library db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	seed := func(fileID, relPath string) {
		if _, err := db.Exec(`INSERT INTO indexed_files
			(id, rel_path, size_bytes, mod_time, status, first_seen_at, last_seen_at, indexed_at)
			VALUES (?, ?, 1, '', 'present', '', '', '')`, fileID, relPath); err != nil {
			t.Fatalf("seed file: %v", err)
		}
	}
	return progress.NewStore(db), seed
}

func TestGet_NoProgressReturnsNotOK(t *testing.T) {
	store, seed := newTestStore(t)
	seed("f1", "book.epub")

	_, ok, err := store.Get(context.Background(), "u1", "f1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if ok {
		t.Error("expected ok=false when no progress recorded")
	}
}

func TestSetAndGetRoundTrip(t *testing.T) {
	store, seed := newTestStore(t)
	seed("f1", "book.epub")
	ctx := context.Background()

	if err := store.Set(ctx, "u1", "f1", "epubcfi(/6/4!/4/2)", 42.5); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, ok, err := store.Get(ctx, "u1", "f1")
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if got.Location != "epubcfi(/6/4!/4/2)" {
		t.Errorf("Location = %q, want the CFI", got.Location)
	}
	if got.Percent != 42.5 {
		t.Errorf("Percent = %v, want 42.5", got.Percent)
	}
	if got.UpdatedAt.IsZero() {
		t.Error("UpdatedAt should be set")
	}
}

func TestSet_UpsertsExistingProgress(t *testing.T) {
	store, seed := newTestStore(t)
	seed("f1", "book.epub")
	ctx := context.Background()

	if err := store.Set(ctx, "u1", "f1", "page:1", 1); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := store.Set(ctx, "u1", "f1", "page:42", 50); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, _, err := store.Get(ctx, "u1", "f1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Location != "page:42" || got.Percent != 50 {
		t.Errorf("got = %+v, want page:42 / 50", got)
	}
}

func TestProgressIsPerUser(t *testing.T) {
	store, seed := newTestStore(t)
	seed("f1", "book.epub")
	ctx := context.Background()

	if err := store.Set(ctx, "u1", "f1", "page:10", 20); err != nil {
		t.Fatalf("Set u1: %v", err)
	}
	_, ok, err := store.Get(ctx, "u2", "f1")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("a second user must not see the first user's reading progress")
	}
}

func TestClear(t *testing.T) {
	store, seed := newTestStore(t)
	seed("f1", "book.epub")
	ctx := context.Background()

	if err := store.Set(ctx, "u1", "f1", "page:5", 10); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := store.Clear(ctx, "u1", "f1"); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	_, ok, err := store.Get(ctx, "u1", "f1")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("expected no progress after Clear")
	}
}

func TestClear_MissingProgressIsNotAnError(t *testing.T) {
	store, seed := newTestStore(t)
	seed("f1", "book.epub")

	if err := store.Clear(context.Background(), "u1", "f1"); err != nil {
		t.Errorf("Clear on unset progress: %v", err)
	}
}
