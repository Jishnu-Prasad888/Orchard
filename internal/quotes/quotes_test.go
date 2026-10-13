package quotes_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Jishnu-Prasad888/Orchard/internal/librarydb"
	"github.com/Jishnu-Prasad888/Orchard/internal/media"
	"github.com/Jishnu-Prasad888/Orchard/internal/quotes"
)

// --- helpers ---

func newTestStore(t *testing.T) (*quotes.Store, *media.FileStore) {
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
	return quotes.NewStore(db), media.NewFileStore(db, "lib1")
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

	q, err := store.Create(ctx, "u1", fileID, quotes.Fields{
		Text:       "To be or not to be",
		Author:     "Shakespeare",
		Chapter:    "Act 3",
		Page:       "42",
		Commentary: "famous line",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if q.ID == "" {
		t.Error("ID is empty")
	}
	if q.Text != "To be or not to be" {
		t.Errorf("Text = %q", q.Text)
	}
	if q.Favorite {
		t.Error("new quote should not be favorited")
	}
	if q.CreatedAt.IsZero() || q.UpdatedAt.IsZero() {
		t.Error("timestamps should be set")
	}
}

func TestCreateEmptyTextError(t *testing.T) {
	store, fs := newTestStore(t)
	ctx := context.Background()
	fileID := seedFile(t, fs, "book.epub")

	_, err := store.Create(ctx, "u1", fileID, quotes.Fields{})
	if err == nil {
		t.Error("expected error for empty text")
	}
}

// --- GetOwned ---

func TestGetOwnedNotFoundForOtherUser(t *testing.T) {
	store, fs := newTestStore(t)
	ctx := context.Background()
	fileID := seedFile(t, fs, "book.epub")

	q, err := store.Create(ctx, "u1", fileID, quotes.Fields{Text: "quote"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.GetOwned(ctx, q.ID, "u2")
	if !errors.Is(err, quotes.ErrNotFound) {
		t.Errorf("expected ErrNotFound for another user's quote, got %v", err)
	}
}

// --- List ---

func TestListScopedPerUser(t *testing.T) {
	store, fs := newTestStore(t)
	ctx := context.Background()
	fileID := seedFile(t, fs, "book.epub")

	if _, err := store.Create(ctx, "u1", fileID, quotes.Fields{Text: "a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(ctx, "u2", fileID, quotes.Fields{Text: "b"}); err != nil {
		t.Fatal(err)
	}

	list, err := store.List(ctx, "u1", quotes.ListOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("len = %d, want 1", len(list))
	}
}

func TestListFilterByFile(t *testing.T) {
	store, fs := newTestStore(t)
	ctx := context.Background()
	fileA := seedFile(t, fs, "a.epub")
	fileB := seedFile(t, fs, "b.epub")

	if _, err := store.Create(ctx, "u1", fileA, quotes.Fields{Text: "a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(ctx, "u1", fileB, quotes.Fields{Text: "b"}); err != nil {
		t.Fatal(err)
	}

	list, err := store.List(ctx, "u1", quotes.ListOptions{FileID: fileA})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].FileID != fileA {
		t.Errorf("List = %v, want only fileA", list)
	}
}

func TestListRespectsLimit(t *testing.T) {
	store, fs := newTestStore(t)
	ctx := context.Background()
	fileID := seedFile(t, fs, "book.epub")

	if _, err := store.Create(ctx, "u1", fileID, quotes.Fields{Text: "a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(ctx, "u1", fileID, quotes.Fields{Text: "b"}); err != nil {
		t.Fatal(err)
	}

	list, err := store.List(ctx, "u1", quotes.ListOptions{Limit: 1})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("len = %d, want 1", len(list))
	}
}

func TestListFavoriteOnly(t *testing.T) {
	store, fs := newTestStore(t)
	ctx := context.Background()
	fileID := seedFile(t, fs, "book.epub")

	q1, _ := store.Create(ctx, "u1", fileID, quotes.Fields{Text: "a"})
	if _, err := store.Create(ctx, "u1", fileID, quotes.Fields{Text: "b"}); err != nil {
		t.Fatal(err)
	}
	favorite := true
	if _, err := store.Update(ctx, q1.ID, "u1", quotes.Update{Favorite: &favorite}); err != nil {
		t.Fatal(err)
	}

	list, err := store.List(ctx, "u1", quotes.ListOptions{FavoriteOnly: true})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].ID != q1.ID {
		t.Errorf("List = %v, want only q1", list)
	}
}

// --- Update ---

func TestUpdateFields(t *testing.T) {
	store, fs := newTestStore(t)
	ctx := context.Background()
	fileID := seedFile(t, fs, "book.epub")

	q, _ := store.Create(ctx, "u1", fileID, quotes.Fields{Text: "old", Author: "old author"})
	newText := "new text"
	updated, err := store.Update(ctx, q.ID, "u1", quotes.Update{Text: &newText})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Text != "new text" {
		t.Errorf("Text = %q, want new text", updated.Text)
	}
	if updated.Author != "old author" {
		t.Errorf("Author should be unchanged, got %q", updated.Author)
	}
}

func TestUpdateEmptyTextError(t *testing.T) {
	store, fs := newTestStore(t)
	ctx := context.Background()
	fileID := seedFile(t, fs, "book.epub")

	q, _ := store.Create(ctx, "u1", fileID, quotes.Fields{Text: "old"})
	empty := ""
	_, err := store.Update(ctx, q.ID, "u1", quotes.Update{Text: &empty})
	if err == nil {
		t.Error("expected error for empty text")
	}
}

func TestUpdateOtherUserNotFound(t *testing.T) {
	store, fs := newTestStore(t)
	ctx := context.Background()
	fileID := seedFile(t, fs, "book.epub")

	q, _ := store.Create(ctx, "u1", fileID, quotes.Fields{Text: "mine"})
	hijack := "hijacked"
	_, err := store.Update(ctx, q.ID, "u2", quotes.Update{Text: &hijack})
	if !errors.Is(err, quotes.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// --- Delete ---

func TestDelete(t *testing.T) {
	store, fs := newTestStore(t)
	ctx := context.Background()
	fileID := seedFile(t, fs, "book.epub")

	q, _ := store.Create(ctx, "u1", fileID, quotes.Fields{Text: "mine"})
	if err := store.Delete(ctx, q.ID, "u1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err := store.GetOwned(ctx, q.ID, "u1")
	if !errors.Is(err, quotes.ErrNotFound) {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestDeleteOtherUserNotFound(t *testing.T) {
	store, fs := newTestStore(t)
	ctx := context.Background()
	fileID := seedFile(t, fs, "book.epub")

	q, _ := store.Create(ctx, "u1", fileID, quotes.Fields{Text: "mine"})
	err := store.Delete(ctx, q.ID, "u2")
	if !errors.Is(err, quotes.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}
