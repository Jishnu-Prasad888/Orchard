package recents_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Jishnu-Prasad888/Orchard/internal/librarydb"
	"github.com/Jishnu-Prasad888/Orchard/internal/media"
	"github.com/Jishnu-Prasad888/Orchard/internal/recents"
)

func newTestStore(t *testing.T) (*recents.Store, *media.FileStore) {
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
	return recents.NewStore(db, "lib1"), media.NewFileStore(db, "lib1")
}

func seedFile(t *testing.T, fs *media.FileStore, relPath string) string {
	t.Helper()
	if err := fs.UpsertFromPath(context.Background(), relPath, 100, time.Now()); err != nil {
		t.Fatalf("seed %s: %v", relPath, err)
	}
	f, err := fs.GetByRelPath(context.Background(), relPath)
	if err != nil {
		t.Fatalf("get seeded file: %v", err)
	}
	return f.ID
}

func TestMarkOpenedAndList(t *testing.T) {
	store, fs := newTestStore(t)
	ctx := context.Background()

	f1 := seedFile(t, fs, "book1.epub")
	f2 := seedFile(t, fs, "book2.epub")

	if err := store.MarkOpened(ctx, "u1", f1); err != nil {
		t.Fatalf("MarkOpened f1: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	if err := store.MarkOpened(ctx, "u1", f2); err != nil {
		t.Fatalf("MarkOpened f2: %v", err)
	}

	got, err := store.ListRecentlyOpened(ctx, "u1", 10)
	if err != nil {
		t.Fatalf("ListRecentlyOpened: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].ID != f2 {
		t.Errorf("most recent = %s, want %s (f2)", got[0].ID, f2)
	}
}

func TestMarkOpenedUpdatesTimestampOnReopen(t *testing.T) {
	store, fs := newTestStore(t)
	ctx := context.Background()

	f1 := seedFile(t, fs, "book1.epub")
	f2 := seedFile(t, fs, "book2.epub")

	if err := store.MarkOpened(ctx, "u1", f1); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	if err := store.MarkOpened(ctx, "u1", f2); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	// Re-open f1: it should now sort first.
	if err := store.MarkOpened(ctx, "u1", f1); err != nil {
		t.Fatal(err)
	}

	got, err := store.ListRecentlyOpened(ctx, "u1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != f1 {
		t.Errorf("got = %+v, want f1 first after reopening it", got)
	}
}

func TestListRecentlyOpenedRespectsLimit(t *testing.T) {
	store, fs := newTestStore(t)
	ctx := context.Background()

	for i := range 5 {
		id := seedFile(t, fs, filepath.Join("books", string(rune('a'+i))+".epub"))
		if err := store.MarkOpened(ctx, "u1", id); err != nil {
			t.Fatal(err)
		}
	}
	got, err := store.ListRecentlyOpened(ctx, "u1", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("len = %d, want 2", len(got))
	}
}

func TestListRecentlyOpenedIsPerUser(t *testing.T) {
	store, fs := newTestStore(t)
	ctx := context.Background()
	f1 := seedFile(t, fs, "book1.epub")

	if err := store.MarkOpened(ctx, "u1", f1); err != nil {
		t.Fatal(err)
	}
	got, err := store.ListRecentlyOpened(ctx, "u2", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("a second user must not see the first user's recently-opened list, got %+v", got)
	}
}

func TestListRecentlyOpenedExcludesTrashed(t *testing.T) {
	store, fs := newTestStore(t)
	ctx := context.Background()
	f1 := seedFile(t, fs, "book1.epub")
	if err := store.MarkOpened(ctx, "u1", f1); err != nil {
		t.Fatal(err)
	}
	if err := fs.DeleteRow(ctx, f1); err != nil {
		t.Fatalf("DeleteRow: %v", err)
	}

	got, err := store.ListRecentlyOpened(ctx, "u1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 recently-opened files after deletion, got %d", len(got))
	}
}
