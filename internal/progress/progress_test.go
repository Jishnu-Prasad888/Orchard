package progress_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

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
		now := time.Now().UTC().Format(time.RFC3339Nano)
		if _, err := db.Exec(`INSERT INTO indexed_files
			(id, rel_path, size_bytes, mod_time, status, first_seen_at, last_seen_at, indexed_at)
			VALUES (?, ?, 1, ?, 'present', ?, ?, ?)`, fileID, relPath, now, now, now, now); err != nil {
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

func TestListInProgress_ExcludesUnstartedAndFinished(t *testing.T) {
	store, seed := newTestStore(t)
	seed("f1", "unstarted.epub")
	seed("f2", "reading.epub")
	seed("f3", "finished.epub")
	ctx := context.Background()

	if err := store.Set(ctx, "u1", "f1", "page:0", 0); err != nil {
		t.Fatalf("Set f1: %v", err)
	}
	if err := store.Set(ctx, "u1", "f2", "page:20", 40); err != nil {
		t.Fatalf("Set f2: %v", err)
	}
	if err := store.Set(ctx, "u1", "f3", "page:100", 100); err != nil {
		t.Fatalf("Set f3: %v", err)
	}

	got, err := store.ListInProgress(ctx, "u1", "lib1", 10)
	if err != nil {
		t.Fatalf("ListInProgress: %v", err)
	}
	if len(got) != 1 || got[0].File.ID != "f2" {
		t.Fatalf("got %+v, want only f2 (0%% and 100%% must be excluded)", got)
	}
	if got[0].File.LibraryID != "lib1" {
		t.Errorf("LibraryID = %q, want lib1", got[0].File.LibraryID)
	}
	if got[0].Percent != 40 {
		t.Errorf("Percent = %v, want 40", got[0].Percent)
	}
}

func TestListInProgress_OrderedByMostRecentAndScopedPerUser(t *testing.T) {
	store, seed := newTestStore(t)
	seed("f1", "a.epub")
	seed("f2", "b.epub")
	ctx := context.Background()

	if err := store.Set(ctx, "u1", "f1", "page:1", 10); err != nil {
		t.Fatalf("Set f1: %v", err)
	}
	if err := store.Set(ctx, "u1", "f2", "page:1", 20); err != nil {
		t.Fatalf("Set f2: %v", err)
	}
	// Touch f1 again so it becomes the most recently updated.
	if err := store.Set(ctx, "u1", "f1", "page:2", 15); err != nil {
		t.Fatalf("Set f1 again: %v", err)
	}

	got, err := store.ListInProgress(ctx, "u1", "lib1", 10)
	if err != nil {
		t.Fatalf("ListInProgress: %v", err)
	}
	if len(got) != 2 || got[0].File.ID != "f1" || got[1].File.ID != "f2" {
		t.Fatalf("got %+v, want [f1, f2] with f1 most recently updated first", got)
	}

	none, err := store.ListInProgress(ctx, "u2", "lib1", 10)
	if err != nil {
		t.Fatalf("ListInProgress u2: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("a second user must not see the first user's in-progress books, got %+v", none)
	}
}

func TestListInProgress_RespectsLimit(t *testing.T) {
	store, seed := newTestStore(t)
	seed("f1", "a.epub")
	seed("f2", "b.epub")
	ctx := context.Background()

	if err := store.Set(ctx, "u1", "f1", "page:1", 10); err != nil {
		t.Fatalf("Set f1: %v", err)
	}
	if err := store.Set(ctx, "u1", "f2", "page:1", 20); err != nil {
		t.Fatalf("Set f2: %v", err)
	}

	got, err := store.ListInProgress(ctx, "u1", "lib1", 1)
	if err != nil {
		t.Fatalf("ListInProgress: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d entries, want 1", len(got))
	}
}

func TestPercents_ReturnsOnlyTheUsersStartedBooks(t *testing.T) {
	store, seed := newTestStore(t)
	seed("f1", "reading.pdf")
	seed("f2", "untouched.pdf")
	seed("f3", "someone-elses.pdf")
	ctx := context.Background()

	if err := store.Set(ctx, "u1", "f1", "page:12", 42.5); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := store.Set(ctx, "u2", "f3", "page:3", 10); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, err := store.Percents(ctx, "u1", []string{"f1", "f2", "f3"})
	if err != nil {
		t.Fatalf("Percents: %v", err)
	}
	if len(got) != 1 || got["f1"] != 42.5 {
		t.Errorf("got %v, want only f1 at 42.5", got)
	}

	empty, err := store.Percents(ctx, "u1", nil)
	if err != nil || len(empty) != 0 {
		t.Errorf("Percents(nil) = %v, %v; want empty, nil", empty, err)
	}
}
