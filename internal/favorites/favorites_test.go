package favorites_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Jishnu-Prasad888/Cairn/internal/favorites"
	"github.com/Jishnu-Prasad888/Cairn/internal/librarydb"
	"github.com/Jishnu-Prasad888/Cairn/internal/media"
)

// --- helpers ---

func newTestStore(t *testing.T) (*favorites.FavoriteStore, *media.FileStore) {
	t.Helper()
	root := t.TempDir()
	cairnDir := filepath.Join(root, ".cairn")
	if err := os.MkdirAll(cairnDir, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := librarydb.Open(cairnDir)
	if err != nil {
		t.Fatalf("open library db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return favorites.NewFavoriteStore(db, "lib1"), media.NewFileStore(db, "lib1")
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

// --- Add ---

func TestAddFavorite(t *testing.T) {
	store, fs := newTestStore(t)
	ctx := context.Background()

	fileID := seedFile(t, fs, "photo.jpg")
	if err := store.Add(ctx, fileID); err != nil {
		t.Fatalf("Add: %v", err)
	}

	ok, err := store.IsFavorite(ctx, fileID)
	if err != nil {
		t.Fatalf("IsFavorite: %v", err)
	}
	if !ok {
		t.Error("expected file to be favorited")
	}
}

func TestAddFavoriteAlreadyFavorited(t *testing.T) {
	store, fs := newTestStore(t)
	ctx := context.Background()

	fileID := seedFile(t, fs, "photo.jpg")
	if err := store.Add(ctx, fileID); err != nil {
		t.Fatalf("first Add: %v", err)
	}

	err := store.Add(ctx, fileID)
	if !errors.Is(err, favorites.ErrAlreadyFavorited) {
		t.Errorf("expected ErrAlreadyFavorited, got %v", err)
	}
}

// --- Remove ---

func TestRemoveFavorite(t *testing.T) {
	store, fs := newTestStore(t)
	ctx := context.Background()

	fileID := seedFile(t, fs, "photo.jpg")
	if err := store.Add(ctx, fileID); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := store.Remove(ctx, fileID); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	ok, _ := store.IsFavorite(ctx, fileID)
	if ok {
		t.Error("expected file to not be favorited after remove")
	}
}

func TestRemoveFavoriteNotFavorited(t *testing.T) {
	store, fs := newTestStore(t)
	ctx := context.Background()

	fileID := seedFile(t, fs, "photo.jpg")
	err := store.Remove(ctx, fileID)
	if !errors.Is(err, favorites.ErrNotFavorited) {
		t.Errorf("expected ErrNotFavorited, got %v", err)
	}
}

// --- IsFavorite ---

func TestIsFavoriteNotFavorited(t *testing.T) {
	store, fs := newTestStore(t)
	ctx := context.Background()

	fileID := seedFile(t, fs, "photo.jpg")
	ok, err := store.IsFavorite(ctx, fileID)
	if err != nil {
		t.Fatalf("IsFavorite: %v", err)
	}
	if ok {
		t.Error("expected false for non-favorited file")
	}
}

// --- ListFiles ---

func TestListFiles(t *testing.T) {
	store, fs := newTestStore(t)
	ctx := context.Background()

	f1 := seedFile(t, fs, "photo1.jpg")
	f2 := seedFile(t, fs, "photo2.jpg")
	seedFile(t, fs, "photo3.jpg") // not favorited

	if err := store.Add(ctx, f1); err != nil {
		t.Fatalf("Add f1: %v", err)
	}
	if err := store.Add(ctx, f2); err != nil {
		t.Fatalf("Add f2: %v", err)
	}

	files, err := store.ListFiles(ctx)
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(files) != 2 {
		t.Errorf("len = %d, want 2", len(files))
	}
}

func TestListFilesEmpty(t *testing.T) {
	store, _ := newTestStore(t)
	files, err := store.ListFiles(context.Background())
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("expected 0 files, got %d", len(files))
	}
}

func TestListFilesHasMetadata(t *testing.T) {
	store, fs := newTestStore(t)
	ctx := context.Background()

	fileID := seedFile(t, fs, "cat.jpg")
	if err := store.Add(ctx, fileID); err != nil {
		t.Fatalf("Add: %v", err)
	}

	files, err := store.ListFiles(ctx)
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("expected 1 file")
	}
	f := files[0]
	if f.ID == "" {
		t.Error("ID is empty")
	}
	if f.Name != "cat.jpg" {
		t.Errorf("Name = %q, want cat.jpg", f.Name)
	}
	if f.LibraryID != "lib1" {
		t.Errorf("LibraryID = %q, want lib1", f.LibraryID)
	}
}

// --- cascade: file deleted removes from favorites ---

func TestFavoriteRemovedWhenFileDeleted(t *testing.T) {
	store, fs := newTestStore(t)
	ctx := context.Background()

	fileID := seedFile(t, fs, "photo.jpg")
	if err := store.Add(ctx, fileID); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Delete the indexed_files row (permanent delete).
	if err := fs.DeleteRow(ctx, fileID); err != nil {
		t.Fatalf("DeleteRow: %v", err)
	}

	files, err := store.ListFiles(ctx)
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("expected 0 favorites after file deletion, got %d", len(files))
	}
}
