package notebook_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Jishnu-Prasad888/Orchard/internal/librarydb"
	"github.com/Jishnu-Prasad888/Orchard/internal/media"
	"github.com/Jishnu-Prasad888/Orchard/internal/notebook"
)

// --- helpers ---

func newTestStore(t *testing.T) (*notebook.Store, *media.FileStore) {
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
	return notebook.NewStore(db), media.NewFileStore(db, "lib1")
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

func TestCreateStandalone(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	n, err := store.Create(ctx, "u1", notebook.Fields{Title: "Ideas", Body: "some thoughts"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if n.ID == "" {
		t.Error("ID is empty")
	}
	if n.FileID != "" || n.QuoteID != "" || n.HighlightID != "" {
		t.Errorf("expected a standalone note, got %+v", n)
	}
	if n.Title != "Ideas" || n.Body != "some thoughts" {
		t.Errorf("Title/Body = %q/%q", n.Title, n.Body)
	}
	if n.CreatedAt.IsZero() || n.UpdatedAt.IsZero() {
		t.Error("timestamps should be set")
	}
}

func TestCreateAboutFile(t *testing.T) {
	store, fs := newTestStore(t)
	ctx := context.Background()
	fileID := seedFile(t, fs, "book.epub")

	n, err := store.Create(ctx, "u1", notebook.Fields{Body: "about this book", FileID: fileID})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if n.FileID != fileID {
		t.Errorf("FileID = %q, want %q", n.FileID, fileID)
	}
}

// --- GetOwned / List ---

func TestGetOwnedNotFoundForOtherUser(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	n, err := store.Create(ctx, "u1", notebook.Fields{Body: "mine"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.GetOwned(ctx, n.ID, "u2")
	if !errors.Is(err, notebook.ErrNotFound) {
		t.Errorf("expected ErrNotFound for another user's note, got %v", err)
	}
}

func TestListScopedPerUser(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	if _, err := store.Create(ctx, "u1", notebook.Fields{Body: "a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(ctx, "u2", notebook.Fields{Body: "b"}); err != nil {
		t.Fatal(err)
	}

	list, err := store.List(ctx, "u1", notebook.ListOptions{})
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

	if _, err := store.Create(ctx, "u1", notebook.Fields{Body: "about a", FileID: fileA}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(ctx, "u1", notebook.Fields{Body: "standalone"}); err != nil {
		t.Fatal(err)
	}

	list, err := store.List(ctx, "u1", notebook.ListOptions{FileID: fileA})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].FileID != fileA {
		t.Errorf("List = %v, want only the note about fileA", list)
	}
}

func TestListStandaloneOnly(t *testing.T) {
	store, fs := newTestStore(t)
	ctx := context.Background()
	fileA := seedFile(t, fs, "a.epub")

	if _, err := store.Create(ctx, "u1", notebook.Fields{Body: "about a", FileID: fileA}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(ctx, "u1", notebook.Fields{Body: "standalone"}); err != nil {
		t.Fatal(err)
	}

	list, err := store.List(ctx, "u1", notebook.ListOptions{Standalone: true})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].Body != "standalone" {
		t.Errorf("List = %v, want only the standalone note", list)
	}
}

// --- cascade / FK behavior ---

func TestFileDeleteCascadesNote(t *testing.T) {
	store, fs := newTestStore(t)
	ctx := context.Background()
	fileID := seedFile(t, fs, "book.epub")

	if _, err := store.Create(ctx, "u1", notebook.Fields{Body: "about this book", FileID: fileID}); err != nil {
		t.Fatal(err)
	}
	if err := fs.DeleteRow(ctx, fileID); err != nil {
		t.Fatal(err)
	}

	list, err := store.List(ctx, "u1", notebook.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Errorf("expected the note to cascade-delete with its file, got %d", len(list))
	}
}

// --- Update ---

func TestUpdateFields(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	n, _ := store.Create(ctx, "u1", notebook.Fields{Title: "old", Body: "old body"})
	newBody := "new body"
	updated, err := store.Update(ctx, n.ID, "u1", notebook.Update{Body: &newBody})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Body != "new body" {
		t.Errorf("Body = %q, want new body", updated.Body)
	}
	if updated.Title != "old" {
		t.Errorf("Title should be unchanged, got %q", updated.Title)
	}
}

func TestUpdateOtherUserNotFound(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	n, _ := store.Create(ctx, "u1", notebook.Fields{Body: "mine"})
	hijack := "hijacked"
	_, err := store.Update(ctx, n.ID, "u2", notebook.Update{Body: &hijack})
	if !errors.Is(err, notebook.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// --- Delete ---

func TestDelete(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	n, _ := store.Create(ctx, "u1", notebook.Fields{Body: "mine"})
	if err := store.Delete(ctx, n.ID, "u1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err := store.GetOwned(ctx, n.ID, "u1")
	if !errors.Is(err, notebook.ErrNotFound) {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestDeleteOtherUserNotFound(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	n, _ := store.Create(ctx, "u1", notebook.Fields{Body: "mine"})
	err := store.Delete(ctx, n.ID, "u2")
	if !errors.Is(err, notebook.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// --- Link / Unlink / ListLinked ---

func TestLinkAndListLinkedBothDirections(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	a, _ := store.Create(ctx, "u1", notebook.Fields{Title: "A"})
	b, _ := store.Create(ctx, "u1", notebook.Fields{Title: "B"})

	if err := store.Link(ctx, "u1", a.ID, b.ID); err != nil {
		t.Fatalf("Link: %v", err)
	}

	linkedFromA, err := store.ListLinked(ctx, "u1", a.ID)
	if err != nil {
		t.Fatalf("ListLinked(a): %v", err)
	}
	if len(linkedFromA) != 1 || linkedFromA[0].ID != b.ID {
		t.Errorf("ListLinked(a) = %v, want [b]", linkedFromA)
	}

	// The link is visible from the other end too (backlink).
	linkedFromB, err := store.ListLinked(ctx, "u1", b.ID)
	if err != nil {
		t.Fatalf("ListLinked(b): %v", err)
	}
	if len(linkedFromB) != 1 || linkedFromB[0].ID != a.ID {
		t.Errorf("ListLinked(b) = %v, want [a]", linkedFromB)
	}
}

func TestLinkOtherUserNoteNotFound(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	a, _ := store.Create(ctx, "u1", notebook.Fields{Title: "A"})
	b, _ := store.Create(ctx, "u2", notebook.Fields{Title: "B"})

	err := store.Link(ctx, "u1", a.ID, b.ID)
	if !errors.Is(err, notebook.ErrNotFound) {
		t.Errorf("expected ErrNotFound linking to another user's note, got %v", err)
	}
}

func TestUnlink(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	a, _ := store.Create(ctx, "u1", notebook.Fields{Title: "A"})
	b, _ := store.Create(ctx, "u1", notebook.Fields{Title: "B"})
	if err := store.Link(ctx, "u1", a.ID, b.ID); err != nil {
		t.Fatal(err)
	}

	if err := store.Unlink(ctx, "u1", a.ID, b.ID); err != nil {
		t.Fatalf("Unlink: %v", err)
	}
	linked, err := store.ListLinked(ctx, "u1", a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(linked) != 0 {
		t.Errorf("expected no linked notes after unlink, got %d", len(linked))
	}
}

func TestDeleteNoteCascadesLinks(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	a, _ := store.Create(ctx, "u1", notebook.Fields{Title: "A"})
	b, _ := store.Create(ctx, "u1", notebook.Fields{Title: "B"})
	if err := store.Link(ctx, "u1", a.ID, b.ID); err != nil {
		t.Fatal(err)
	}

	if err := store.Delete(ctx, b.ID, "u1"); err != nil {
		t.Fatal(err)
	}
	linked, err := store.ListLinked(ctx, "u1", a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(linked) != 0 {
		t.Errorf("expected the link to cascade-delete with the note, got %d", len(linked))
	}
}
