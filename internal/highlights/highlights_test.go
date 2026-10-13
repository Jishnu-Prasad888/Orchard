package highlights_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Jishnu-Prasad888/Orchard/internal/highlights"
	"github.com/Jishnu-Prasad888/Orchard/internal/librarydb"
	"github.com/Jishnu-Prasad888/Orchard/internal/media"
)

// --- helpers ---

func newTestStore(t *testing.T) (*highlights.Store, *media.FileStore) {
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
	return highlights.NewStore(db), media.NewFileStore(db, "lib1")
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

	h, err := store.Create(ctx, "u1", fileID, "epubcfi(/6/14!/4/2)", "a passage", "", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if h.ID == "" {
		t.Error("ID is empty")
	}
	if h.Text != "a passage" {
		t.Errorf("Text = %q", h.Text)
	}
	if h.Color != highlights.DefaultColor {
		t.Errorf("Color = %q, want default %q", h.Color, highlights.DefaultColor)
	}
	if h.CreatedAt.IsZero() || h.UpdatedAt.IsZero() {
		t.Error("timestamps should be set")
	}
}

func TestCreateCustomColor(t *testing.T) {
	store, fs := newTestStore(t)
	ctx := context.Background()
	fileID := seedFile(t, fs, "book.epub")

	h, err := store.Create(ctx, "u1", fileID, "page:1", "text", "blue", "note")
	if err != nil {
		t.Fatal(err)
	}
	if h.Color != "blue" {
		t.Errorf("Color = %q, want blue", h.Color)
	}
	if h.Note != "note" {
		t.Errorf("Note = %q", h.Note)
	}
}

func TestCreateEmptyLocationError(t *testing.T) {
	store, fs := newTestStore(t)
	ctx := context.Background()
	fileID := seedFile(t, fs, "book.epub")

	_, err := store.Create(ctx, "u1", fileID, "", "text", "", "")
	if err == nil {
		t.Error("expected error for empty location")
	}
}

// --- GetOwned / List ---

func TestGetOwnedNotFoundForOtherUser(t *testing.T) {
	store, fs := newTestStore(t)
	ctx := context.Background()
	fileID := seedFile(t, fs, "book.epub")

	h, err := store.Create(ctx, "u1", fileID, "page:1", "text", "", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.GetOwned(ctx, h.ID, "u2")
	if !errors.Is(err, highlights.ErrNotFound) {
		t.Errorf("expected ErrNotFound for another user's highlight, got %v", err)
	}
}

func TestListScopedPerUser(t *testing.T) {
	store, fs := newTestStore(t)
	ctx := context.Background()
	fileID := seedFile(t, fs, "book.epub")

	if _, err := store.Create(ctx, "u1", fileID, "page:1", "a", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(ctx, "u2", fileID, "page:2", "b", "", ""); err != nil {
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

func TestListByFile(t *testing.T) {
	store, fs := newTestStore(t)
	ctx := context.Background()
	fileA := seedFile(t, fs, "a.epub")
	fileB := seedFile(t, fs, "b.epub")

	if _, err := store.Create(ctx, "u1", fileA, "page:1", "a", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(ctx, "u1", fileB, "page:1", "b", "", ""); err != nil {
		t.Fatal(err)
	}

	list, err := store.ListByFile(ctx, "u1", fileA)
	if err != nil {
		t.Fatalf("ListByFile: %v", err)
	}
	if len(list) != 1 || list[0].FileID != fileA {
		t.Errorf("ListByFile = %v, want only fileA", list)
	}
}

// --- Update ---

func TestUpdateNoteAndColor(t *testing.T) {
	store, fs := newTestStore(t)
	ctx := context.Background()
	fileID := seedFile(t, fs, "book.epub")

	h, _ := store.Create(ctx, "u1", fileID, "page:1", "text", "yellow", "")
	newColor := "green"
	newNote := "updated note"
	updated, err := store.Update(ctx, h.ID, "u1", highlights.Update{Color: &newColor, Note: &newNote})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Color != "green" || updated.Note != "updated note" {
		t.Errorf("got color=%q note=%q", updated.Color, updated.Note)
	}
}

func TestUpdateOtherUserNotFound(t *testing.T) {
	store, fs := newTestStore(t)
	ctx := context.Background()
	fileID := seedFile(t, fs, "book.epub")

	h, _ := store.Create(ctx, "u1", fileID, "page:1", "text", "", "")
	newNote := "hijacked"
	_, err := store.Update(ctx, h.ID, "u2", highlights.Update{Note: &newNote})
	if !errors.Is(err, highlights.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// --- Delete ---

func TestDelete(t *testing.T) {
	store, fs := newTestStore(t)
	ctx := context.Background()
	fileID := seedFile(t, fs, "book.epub")

	h, _ := store.Create(ctx, "u1", fileID, "page:1", "text", "", "")
	if err := store.Delete(ctx, h.ID, "u1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err := store.GetOwned(ctx, h.ID, "u1")
	if !errors.Is(err, highlights.ErrNotFound) {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestDeleteOtherUserNotFound(t *testing.T) {
	store, fs := newTestStore(t)
	ctx := context.Background()
	fileID := seedFile(t, fs, "book.epub")

	h, _ := store.Create(ctx, "u1", fileID, "page:1", "text", "", "")
	err := store.Delete(ctx, h.ID, "u2")
	if !errors.Is(err, highlights.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}
