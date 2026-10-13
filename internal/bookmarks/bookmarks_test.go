package bookmarks_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Jishnu-Prasad888/Orchard/internal/bookmarks"
	"github.com/Jishnu-Prasad888/Orchard/internal/librarydb"
	"github.com/Jishnu-Prasad888/Orchard/internal/media"
)

// --- helpers ---

func newTestStore(t *testing.T) (*bookmarks.Store, *media.FileStore) {
	t.Helper()
	root := t.TempDir()
	orchardDir := filepath.Join(root, ".orchard")
	if err := os.MkdirAll(orchardDir, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := librarydb.Open(orchardDir)
	if err != nil {
		t.Fatalf("open library db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return bookmarks.NewStore(db), media.NewFileStore(db, "lib1")
}

func seedFile(t *testing.T, fs *media.FileStore, relPath string) string {
	t.Helper()
	if err := fs.UpsertFromPath(context.Background(), relPath, 100, time.Now()); err != nil {
		t.Fatalf("seed %s: %v", relPath, err)
	}
	f, err := fs.GetByRelPath(context.Background(), relPath)
	if err != nil {
		t.Fatalf("get seeded file %s: %v", relPath, err)
	}
	return f.ID
}

// --- Create ---

func TestCreate(t *testing.T) {
	store, fs := newTestStore(t)
	ctx := context.Background()
	fileID := seedFile(t, fs, "book.epub")

	b, err := store.Create(ctx, "u1", fileID, "page:42", "Chapter 3 start")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if b.ID == "" {
		t.Error("ID is empty")
	}
	if b.UserID != "u1" || b.FileID != fileID {
		t.Errorf("UserID/FileID = %q/%q", b.UserID, b.FileID)
	}
	if b.Location != "page:42" {
		t.Errorf("Location = %q", b.Location)
	}
	if b.Label != "Chapter 3 start" {
		t.Errorf("Label = %q", b.Label)
	}
	if b.CreatedAt.IsZero() {
		t.Error("CreatedAt is zero")
	}
}

func TestCreateEmptyLocationError(t *testing.T) {
	store, fs := newTestStore(t)
	ctx := context.Background()
	fileID := seedFile(t, fs, "book.epub")

	_, err := store.Create(ctx, "u1", fileID, "", "label")
	if err == nil {
		t.Error("expected error for empty location")
	}
}

func TestCreateManyPerFile(t *testing.T) {
	store, fs := newTestStore(t)
	ctx := context.Background()
	fileID := seedFile(t, fs, "book.epub")

	if _, err := store.Create(ctx, "u1", fileID, "page:1", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(ctx, "u1", fileID, "page:2", ""); err != nil {
		t.Fatal(err)
	}
	list, err := store.ListByFile(ctx, "u1", fileID)
	if err != nil {
		t.Fatalf("ListByFile: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("len = %d, want 2", len(list))
	}
}

// --- GetOwned ---

func TestGetOwnedNotFoundForOtherUser(t *testing.T) {
	store, fs := newTestStore(t)
	ctx := context.Background()
	fileID := seedFile(t, fs, "book.epub")

	b, err := store.Create(ctx, "u1", fileID, "page:1", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.GetOwned(ctx, b.ID, "u2")
	if !errors.Is(err, bookmarks.ErrNotFound) {
		t.Errorf("expected ErrNotFound for another user's bookmark, got %v", err)
	}
}

func TestGetOwnedNotFoundGhost(t *testing.T) {
	store, _ := newTestStore(t)
	_, err := store.GetOwned(context.Background(), "ghost", "u1")
	if !errors.Is(err, bookmarks.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// --- List ---

func TestListScopedPerUser(t *testing.T) {
	store, fs := newTestStore(t)
	ctx := context.Background()
	fileID := seedFile(t, fs, "book.epub")

	if _, err := store.Create(ctx, "u1", fileID, "page:1", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(ctx, "u2", fileID, "page:2", ""); err != nil {
		t.Fatal(err)
	}

	list, err := store.List(ctx, "u1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("len = %d, want 1", len(list))
	}
}

// --- UpdateLabel ---

func TestUpdateLabel(t *testing.T) {
	store, fs := newTestStore(t)
	ctx := context.Background()
	fileID := seedFile(t, fs, "book.epub")

	b, _ := store.Create(ctx, "u1", fileID, "page:1", "old")
	updated, err := store.UpdateLabel(ctx, b.ID, "u1", "new")
	if err != nil {
		t.Fatalf("UpdateLabel: %v", err)
	}
	if updated.Label != "new" {
		t.Errorf("Label = %q, want new", updated.Label)
	}
}

func TestUpdateLabelOtherUserNotFound(t *testing.T) {
	store, fs := newTestStore(t)
	ctx := context.Background()
	fileID := seedFile(t, fs, "book.epub")

	b, _ := store.Create(ctx, "u1", fileID, "page:1", "old")
	_, err := store.UpdateLabel(ctx, b.ID, "u2", "hijacked")
	if !errors.Is(err, bookmarks.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// --- Delete ---

func TestDelete(t *testing.T) {
	store, fs := newTestStore(t)
	ctx := context.Background()
	fileID := seedFile(t, fs, "book.epub")

	b, _ := store.Create(ctx, "u1", fileID, "page:1", "")
	if err := store.Delete(ctx, b.ID, "u1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err := store.GetOwned(ctx, b.ID, "u1")
	if !errors.Is(err, bookmarks.ErrNotFound) {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestDeleteOtherUserNotFound(t *testing.T) {
	store, fs := newTestStore(t)
	ctx := context.Background()
	fileID := seedFile(t, fs, "book.epub")

	b, _ := store.Create(ctx, "u1", fileID, "page:1", "")
	err := store.Delete(ctx, b.ID, "u2")
	if !errors.Is(err, bookmarks.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
	if _, err := store.GetOwned(ctx, b.ID, "u1"); err != nil {
		t.Errorf("bookmark should survive another user's failed delete: %v", err)
	}
}

func TestDeleteCascadesWhenFileRemoved(t *testing.T) {
	store, fs := newTestStore(t)
	ctx := context.Background()
	fileID := seedFile(t, fs, "book.epub")

	if _, err := store.Create(ctx, "u1", fileID, "page:1", ""); err != nil {
		t.Fatal(err)
	}
	if err := fs.DeleteRow(ctx, fileID); err != nil {
		t.Fatal(err)
	}
	list, err := store.List(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Errorf("expected bookmarks to cascade-delete with file, got %d", len(list))
	}
}
