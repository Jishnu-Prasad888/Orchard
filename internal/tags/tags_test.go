package tags_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Jishnu-Prasad888/Cairn/internal/librarydb"
	"github.com/Jishnu-Prasad888/Cairn/internal/media"
	"github.com/Jishnu-Prasad888/Cairn/internal/tags"
)

// --- helpers ---

func newTestStore(t *testing.T) *tags.TagStore {
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
	return tags.NewTagStore(db)
}

func newTestStoreWithFiles(t *testing.T) (*tags.TagStore, *media.FileStore) {
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
	return tags.NewTagStore(db), media.NewFileStore(db, "lib1")
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

func TestCreateTag(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	tag, err := store.Create(ctx, "nature", "#00ff00")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if tag.ID == "" {
		t.Error("tag ID is empty")
	}
	if tag.Name != "nature" {
		t.Errorf("name = %q, want nature", tag.Name)
	}
	if tag.Color != "#00ff00" {
		t.Errorf("color = %q, want #00ff00", tag.Color)
	}
	if tag.CreatedAt.IsZero() {
		t.Error("CreatedAt is zero")
	}
}

func TestCreateTagWithoutColor(t *testing.T) {
	store := newTestStore(t)
	tag, err := store.Create(context.Background(), "plain", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if tag.Color != "" {
		t.Errorf("color = %q, want empty", tag.Color)
	}
}

func TestCreateTagEmptyNameError(t *testing.T) {
	store := newTestStore(t)
	_, err := store.Create(context.Background(), "", "")
	if err == nil {
		t.Error("expected error for empty name")
	}
}

func TestCreateTagDuplicateNameError(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	_, err := store.Create(ctx, "travel", "")
	if err != nil {
		t.Fatalf("first Create: %v", err)
	}
	_, err = store.Create(ctx, "travel", "")
	if !errors.Is(err, tags.ErrAlreadyExists) {
		t.Errorf("expected ErrAlreadyExists, got %v", err)
	}
}

func TestCreateTagCaseInsensitiveDuplicate(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	_, err := store.Create(ctx, "Travel", "")
	if err != nil {
		t.Fatalf("first Create: %v", err)
	}
	_, err = store.Create(ctx, "TRAVEL", "")
	if !errors.Is(err, tags.ErrAlreadyExists) {
		t.Errorf("expected ErrAlreadyExists for case-insensitive dup, got %v", err)
	}
}

// --- GetByID / GetByName ---

func TestGetByID(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	created, _ := store.Create(ctx, "test-tag", "")
	got, err := store.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("ID mismatch: %q != %q", got.ID, created.ID)
	}
}

func TestGetByIDNotFound(t *testing.T) {
	store := newTestStore(t)
	_, err := store.GetByID(context.Background(), "nonexistent")
	if !errors.Is(err, tags.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestGetByName(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	if _, err := store.Create(ctx, "myTag", ""); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetByName(ctx, "mytag") // case-insensitive
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if got.Name != "myTag" {
		t.Errorf("Name = %q, want myTag", got.Name)
	}
}

// --- List ---

func TestList(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	if _, err := store.Create(ctx, "beta", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(ctx, "alpha", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(ctx, "gamma", ""); err != nil {
		t.Fatal(err)
	}

	ts, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(ts) != 3 {
		t.Fatalf("len = %d, want 3", len(ts))
	}
	// Should be sorted by name.
	if ts[0].Name != "alpha" || ts[1].Name != "beta" || ts[2].Name != "gamma" {
		t.Errorf("order: got %v, want alpha/beta/gamma", func() []string {
			names := make([]string, len(ts))
			for i, tag := range ts {
				names[i] = tag.Name
			}
			return names
		}())
	}
}

func TestListEmpty(t *testing.T) {
	store := newTestStore(t)
	ts, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(ts) != 0 {
		t.Errorf("expected empty list, got %v", ts)
	}
}

// --- Delete ---

func TestDelete(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	tag, _ := store.Create(ctx, "to-delete", "")
	if err := store.Delete(ctx, tag.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err := store.GetByID(ctx, tag.ID)
	if !errors.Is(err, tags.ErrNotFound) {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestDeleteNotFound(t *testing.T) {
	store := newTestStore(t)
	err := store.Delete(context.Background(), "ghost")
	if !errors.Is(err, tags.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// --- Attach / Detach ---

func TestAttachDetach(t *testing.T) {
	store, fs := newTestStoreWithFiles(t)
	ctx := context.Background()

	fileID := seedFile(t, fs, "photo.jpg")
	tag, _ := store.Create(ctx, "nature", "")

	// Attach
	if err := store.Attach(ctx, fileID, tag.ID); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	// Verify tag is attached.
	ts, err := store.ListByFile(ctx, fileID)
	if err != nil {
		t.Fatalf("ListByFile: %v", err)
	}
	if len(ts) != 1 || ts[0].ID != tag.ID {
		t.Errorf("ListByFile = %v, want 1 tag with ID %q", ts, tag.ID)
	}

	// Detach
	if err := store.Detach(ctx, fileID, tag.ID); err != nil {
		t.Fatalf("Detach: %v", err)
	}

	ts2, _ := store.ListByFile(ctx, fileID)
	if len(ts2) != 0 {
		t.Errorf("expected 0 tags after detach, got %d", len(ts2))
	}
}

func TestAttachIdempotent(t *testing.T) {
	store, fs := newTestStoreWithFiles(t)
	ctx := context.Background()

	fileID := seedFile(t, fs, "photo.jpg")
	tag, _ := store.Create(ctx, "nature", "")

	// Attach twice should be no-op (INSERT OR IGNORE).
	if err := store.Attach(ctx, fileID, tag.ID); err != nil {
		t.Fatalf("first Attach: %v", err)
	}
	if err := store.Attach(ctx, fileID, tag.ID); err != nil {
		t.Fatalf("second Attach: %v", err)
	}
	ts, _ := store.ListByFile(ctx, fileID)
	if len(ts) != 1 {
		t.Errorf("expected 1 tag after double attach, got %d", len(ts))
	}
}

func TestDetachNotAttached(t *testing.T) {
	store, fs := newTestStoreWithFiles(t)
	ctx := context.Background()

	fileID := seedFile(t, fs, "photo.jpg")
	tag, _ := store.Create(ctx, "nature", "")

	err := store.Detach(ctx, fileID, tag.ID)
	if !errors.Is(err, tags.ErrFileNotTagged) {
		t.Errorf("expected ErrFileNotTagged, got %v", err)
	}
}

// --- ListByFile / ListByTag ---

func TestListByFile(t *testing.T) {
	store, fs := newTestStoreWithFiles(t)
	ctx := context.Background()

	fileID := seedFile(t, fs, "photo.jpg")
	t1, _ := store.Create(ctx, "nature", "")
	t2, _ := store.Create(ctx, "travel", "")

	if err := store.Attach(ctx, fileID, t1.ID); err != nil {
		t.Fatalf("Attach t1: %v", err)
	}
	if err := store.Attach(ctx, fileID, t2.ID); err != nil {
		t.Fatalf("Attach t2: %v", err)
	}

	ts, err := store.ListByFile(ctx, fileID)
	if err != nil {
		t.Fatalf("ListByFile: %v", err)
	}
	if len(ts) != 2 {
		t.Errorf("len = %d, want 2", len(ts))
	}
}

func TestListByFileEmpty(t *testing.T) {
	store, fs := newTestStoreWithFiles(t)
	ctx := context.Background()

	fileID := seedFile(t, fs, "photo.jpg")
	ts, err := store.ListByFile(ctx, fileID)
	if err != nil {
		t.Fatalf("ListByFile: %v", err)
	}
	if len(ts) != 0 {
		t.Errorf("expected 0 tags, got %d", len(ts))
	}
}

func TestListByTag(t *testing.T) {
	store, fs := newTestStoreWithFiles(t)
	ctx := context.Background()

	f1 := seedFile(t, fs, "a.jpg")
	f2 := seedFile(t, fs, "b.jpg")
	tag, _ := store.Create(ctx, "shared-tag", "")

	if err := store.Attach(ctx, f1, tag.ID); err != nil {
		t.Fatalf("Attach f1: %v", err)
	}
	if err := store.Attach(ctx, f2, tag.ID); err != nil {
		t.Fatalf("Attach f2: %v", err)
	}

	fileIDs, err := store.ListByTag(ctx, tag.ID)
	if err != nil {
		t.Fatalf("ListByTag: %v", err)
	}
	if len(fileIDs) != 2 {
		t.Errorf("len = %d, want 2", len(fileIDs))
	}
}

// --- cascade delete ---

func TestDeleteTagCascades(t *testing.T) {
	store, fs := newTestStoreWithFiles(t)
	ctx := context.Background()

	fileID := seedFile(t, fs, "photo.jpg")
	tag, _ := store.Create(ctx, "cascade-tag", "")
	if err := store.Attach(ctx, fileID, tag.ID); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	// Delete the tag; file_tags row should be gone too.
	if err := store.Delete(ctx, tag.ID); err != nil {
		t.Fatal(err)
	}
	ts, _ := store.ListByFile(ctx, fileID)
	if len(ts) != 0 {
		t.Errorf("expected 0 tags after cascade delete, got %d", len(ts))
	}
}
