package collections_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Jishnu-Prasad888/Orchard/internal/collections"
	"github.com/Jishnu-Prasad888/Orchard/internal/librarydb"
	"github.com/Jishnu-Prasad888/Orchard/internal/media"
)

// --- helpers ---

func newTestStore(t *testing.T) *collections.Store {
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
	return collections.NewStore(db, "lib1")
}

func newTestStoreWithFiles(t *testing.T) (*collections.Store, *media.FileStore) {
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
	return collections.NewStore(db, "lib1"), media.NewFileStore(db, "lib1")
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
	store := newTestStore(t)
	ctx := context.Background()

	c, err := store.Create(ctx, "u1", "Summer reading", "beach books")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if c.ID == "" {
		t.Error("ID is empty")
	}
	if c.UserID != "u1" {
		t.Errorf("UserID = %q, want u1", c.UserID)
	}
	if c.Name != "Summer reading" {
		t.Errorf("Name = %q", c.Name)
	}
	if c.Description != "beach books" {
		t.Errorf("Description = %q", c.Description)
	}
	if c.Pinned {
		t.Error("new collection should not be pinned")
	}
	if c.ItemCount != 0 {
		t.Errorf("ItemCount = %d, want 0", c.ItemCount)
	}
	if c.CreatedAt.IsZero() {
		t.Error("CreatedAt is zero")
	}
}

func TestCreateEmptyNameError(t *testing.T) {
	store := newTestStore(t)
	_, err := store.Create(context.Background(), "u1", "", "")
	if err == nil {
		t.Error("expected error for empty name")
	}
}

func TestCreateDuplicateNameSameUserError(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	if _, err := store.Create(ctx, "u1", "Travel", ""); err != nil {
		t.Fatal(err)
	}
	_, err := store.Create(ctx, "u1", "travel", "") // case-insensitive
	if !errors.Is(err, collections.ErrAlreadyExists) {
		t.Errorf("expected ErrAlreadyExists, got %v", err)
	}
}

func TestCreateSameNameDifferentUsersOK(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	if _, err := store.Create(ctx, "u1", "Travel", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(ctx, "u2", "Travel", ""); err != nil {
		t.Errorf("different users should be able to reuse a name: %v", err)
	}
}

// --- GetOwned ---

func TestGetOwnedNotFoundForOtherUser(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	c, err := store.Create(ctx, "u1", "Mine", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.GetOwned(ctx, c.ID, "u2")
	if !errors.Is(err, collections.ErrNotFound) {
		t.Errorf("expected ErrNotFound for another user's collection, got %v", err)
	}
}

func TestGetOwnedNotFoundGhost(t *testing.T) {
	store := newTestStore(t)
	_, err := store.GetOwned(context.Background(), "ghost", "u1")
	if !errors.Is(err, collections.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// --- List ---

func TestListScopedPerUser(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	if _, err := store.Create(ctx, "u1", "Beta", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(ctx, "u1", "Alpha", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(ctx, "u2", "Other user's", ""); err != nil {
		t.Fatal(err)
	}

	list, err := store.List(ctx, "u1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("len = %d, want 2", len(list))
	}
	if list[0].Name != "Alpha" || list[1].Name != "Beta" {
		t.Errorf("expected alphabetical order, got %s, %s", list[0].Name, list[1].Name)
	}
}

func TestListPinnedFirst(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	a, _ := store.Create(ctx, "u1", "Zebra", "")
	_, _ = store.Create(ctx, "u1", "Apple", "")
	pinned := true
	if _, err := store.Update(ctx, a.ID, "u1", collections.Update{Pinned: &pinned}); err != nil {
		t.Fatal(err)
	}

	list, err := store.List(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if list[0].Name != "Zebra" || !list[0].Pinned {
		t.Errorf("expected pinned Zebra first, got %+v", list[0])
	}
}

// --- Update ---

func TestUpdateRename(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	c, _ := store.Create(ctx, "u1", "Old name", "old desc")
	newName := "New name"
	updated, err := store.Update(ctx, c.ID, "u1", collections.Update{Name: &newName})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Name != "New name" {
		t.Errorf("Name = %q, want New name", updated.Name)
	}
	if updated.Description != "old desc" {
		t.Errorf("Description should be unchanged, got %q", updated.Description)
	}
}

func TestUpdateOtherUserNotFound(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	c, _ := store.Create(ctx, "u1", "Mine", "")
	newName := "Hijacked"
	_, err := store.Update(ctx, c.ID, "u2", collections.Update{Name: &newName})
	if !errors.Is(err, collections.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// --- Delete ---

func TestDelete(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	c, _ := store.Create(ctx, "u1", "to-delete", "")
	if err := store.Delete(ctx, c.ID, "u1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err := store.GetOwned(ctx, c.ID, "u1")
	if !errors.Is(err, collections.ErrNotFound) {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestDeleteOtherUserNotFound(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	c, _ := store.Create(ctx, "u1", "Mine", "")
	err := store.Delete(ctx, c.ID, "u2")
	if !errors.Is(err, collections.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
	// And it should still exist for the owner.
	if _, err := store.GetOwned(ctx, c.ID, "u1"); err != nil {
		t.Errorf("collection should survive another user's failed delete: %v", err)
	}
}

// --- AddFile / RemoveFile / ListFiles ---

func TestAddAndListFiles(t *testing.T) {
	store, fs := newTestStoreWithFiles(t)
	ctx := context.Background()

	fileID := seedFile(t, fs, "book.epub")
	c, _ := store.Create(ctx, "u1", "Reading list", "")

	if err := store.AddFile(ctx, c.ID, "u1", fileID); err != nil {
		t.Fatalf("AddFile: %v", err)
	}

	files, err := store.ListFiles(ctx, c.ID, "u1")
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(files) != 1 || files[0].ID != fileID {
		t.Errorf("ListFiles = %v, want [%s]", files, fileID)
	}

	got, _ := store.GetOwned(ctx, c.ID, "u1")
	if got.ItemCount != 1 {
		t.Errorf("ItemCount = %d, want 1", got.ItemCount)
	}
}

func TestAddFileIdempotent(t *testing.T) {
	store, fs := newTestStoreWithFiles(t)
	ctx := context.Background()

	fileID := seedFile(t, fs, "book.epub")
	c, _ := store.Create(ctx, "u1", "Reading list", "")

	if err := store.AddFile(ctx, c.ID, "u1", fileID); err != nil {
		t.Fatalf("first AddFile: %v", err)
	}
	if err := store.AddFile(ctx, c.ID, "u1", fileID); err != nil {
		t.Fatalf("second AddFile: %v", err)
	}
	files, _ := store.ListFiles(ctx, c.ID, "u1")
	if len(files) != 1 {
		t.Errorf("expected 1 file after double add, got %d", len(files))
	}
}

func TestAddFileOtherUserNotFound(t *testing.T) {
	store, fs := newTestStoreWithFiles(t)
	ctx := context.Background()

	fileID := seedFile(t, fs, "book.epub")
	c, _ := store.Create(ctx, "u1", "Mine", "")

	err := store.AddFile(ctx, c.ID, "u2", fileID)
	if !errors.Is(err, collections.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestRemoveFile(t *testing.T) {
	store, fs := newTestStoreWithFiles(t)
	ctx := context.Background()

	fileID := seedFile(t, fs, "book.epub")
	c, _ := store.Create(ctx, "u1", "Reading list", "")
	if err := store.AddFile(ctx, c.ID, "u1", fileID); err != nil {
		t.Fatal(err)
	}

	if err := store.RemoveFile(ctx, c.ID, "u1", fileID); err != nil {
		t.Fatalf("RemoveFile: %v", err)
	}
	files, _ := store.ListFiles(ctx, c.ID, "u1")
	if len(files) != 0 {
		t.Errorf("expected 0 files after remove, got %d", len(files))
	}
}

func TestRemoveFileNotInCollection(t *testing.T) {
	store, fs := newTestStoreWithFiles(t)
	ctx := context.Background()

	fileID := seedFile(t, fs, "book.epub")
	c, _ := store.Create(ctx, "u1", "Reading list", "")

	err := store.RemoveFile(ctx, c.ID, "u1", fileID)
	if !errors.Is(err, collections.ErrFileNotInCollection) {
		t.Errorf("expected ErrFileNotInCollection, got %v", err)
	}
}

// --- ListByFile ---

func TestListByFile(t *testing.T) {
	store, fs := newTestStoreWithFiles(t)
	ctx := context.Background()

	fileID := seedFile(t, fs, "book.epub")
	c1, _ := store.Create(ctx, "u1", "List A", "")
	c2, _ := store.Create(ctx, "u1", "List B", "")
	_, _ = store.Create(ctx, "u1", "List C", "") // does not contain the file

	if err := store.AddFile(ctx, c1.ID, "u1", fileID); err != nil {
		t.Fatal(err)
	}
	if err := store.AddFile(ctx, c2.ID, "u1", fileID); err != nil {
		t.Fatal(err)
	}

	byFile, err := store.ListByFile(ctx, "u1", fileID)
	if err != nil {
		t.Fatalf("ListByFile: %v", err)
	}
	if len(byFile) != 2 {
		t.Errorf("len = %d, want 2", len(byFile))
	}
}

// --- cascade delete ---

func TestDeleteCollectionCascadesItems(t *testing.T) {
	store, fs := newTestStoreWithFiles(t)
	ctx := context.Background()

	fileID := seedFile(t, fs, "book.epub")
	c, _ := store.Create(ctx, "u1", "Reading list", "")
	if err := store.AddFile(ctx, c.ID, "u1", fileID); err != nil {
		t.Fatal(err)
	}

	if err := store.Delete(ctx, c.ID, "u1"); err != nil {
		t.Fatal(err)
	}

	byFile, err := store.ListByFile(ctx, "u1", fileID)
	if err != nil {
		t.Fatal(err)
	}
	if len(byFile) != 0 {
		t.Errorf("expected 0 collections after cascade delete, got %d", len(byFile))
	}
}

func TestListFilesExcludesNonPresent(t *testing.T) {
	store, fs := newTestStoreWithFiles(t)
	ctx := context.Background()

	fileID := seedFile(t, fs, "book.epub")
	c, _ := store.Create(ctx, "u1", "Reading list", "")
	if err := store.AddFile(ctx, c.ID, "u1", fileID); err != nil {
		t.Fatal(err)
	}
	if err := fs.MarkDeleted(ctx, fileID); err != nil {
		t.Fatal(err)
	}

	files, err := store.ListFiles(ctx, c.ID, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Errorf("expected deleted file to be excluded, got %d", len(files))
	}
}

// --- Timeline ---

func TestTimelineOrderReplaceAndOwnership(t *testing.T) {
	store, fs := newTestStoreWithFiles(t)
	ctx := context.Background()
	c, err := store.Create(ctx, "u1", "Series", "")
	if err != nil {
		t.Fatal(err)
	}
	a, b, d := seedFile(t, fs, "a.epub"), seedFile(t, fs, "b.epub"), seedFile(t, fs, "d.epub")
	outside := seedFile(t, fs, "outside.epub")
	for _, id := range []string{a, b, d} {
		if err := store.AddFile(ctx, c.ID, "u1", id); err != nil {
			t.Fatal(err)
		}
	}

	if got, _ := store.Timeline(ctx, c.ID, "u1"); len(got) != 0 {
		t.Fatalf("new timeline = %v, want empty", got)
	}
	if err := store.SetTimeline(ctx, c.ID, "u1", []string{d, a, d}); err != nil {
		t.Fatal(err)
	}
	got, err := store.Timeline(ctx, c.ID, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != d+","+a {
		t.Errorf("timeline = %v, want [d a]", got)
	}

	// Replacing drops what is left out.
	if err := store.SetTimeline(ctx, c.ID, "u1", []string{b}); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.Timeline(ctx, c.ID, "u1"); strings.Join(got, ",") != b {
		t.Errorf("timeline after replace = %v, want [b]", got)
	}

	// A book outside the collection is refused, and nothing changes.
	if err := store.SetTimeline(ctx, c.ID, "u1", []string{a, outside}); !errors.Is(err, collections.ErrFileNotInCollection) {
		t.Errorf("outside book: err = %v, want ErrFileNotInCollection", err)
	}
	if got, _ := store.Timeline(ctx, c.ID, "u1"); strings.Join(got, ",") != b {
		t.Errorf("timeline after refused set = %v, want [b]", got)
	}

	// Another user's collection is not found.
	if err := store.SetTimeline(ctx, c.ID, "u2", []string{a}); !errors.Is(err, collections.ErrNotFound) {
		t.Errorf("other user: err = %v, want ErrNotFound", err)
	}
}

// --- Colour ---

func TestUpdateColor(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	c, err := store.Create(ctx, "u1", "Sci-fi", "")
	if err != nil {
		t.Fatal(err)
	}
	if c.Color != "" {
		t.Fatalf("new collection colour = %q, want none", c.Color)
	}
	mint := "mint"
	got, err := store.Update(ctx, c.ID, "u1", collections.Update{Color: &mint})
	if err != nil || got.Color != "mint" {
		t.Fatalf("set colour: %v, %+v", err, got)
	}
	// Leaving Color out keeps it.
	pinned := true
	if got, _ = store.Update(ctx, c.ID, "u1", collections.Update{Pinned: &pinned}); got.Color != "mint" {
		t.Errorf("colour after pin = %q, want mint", got.Color)
	}
	bad := "chartreuse"
	if _, err := store.Update(ctx, c.ID, "u1", collections.Update{Color: &bad}); !errors.Is(err, collections.ErrInvalidColor) {
		t.Errorf("unknown colour: err = %v, want ErrInvalidColor", err)
	}
	none := ""
	if got, _ = store.Update(ctx, c.ID, "u1", collections.Update{Color: &none}); got.Color != "" {
		t.Errorf("cleared colour = %q, want none", got.Color)
	}
}

// TestListPinnedMostRecentFirst: pinned collections lead, the one pinned
// last at the front; unpinning drops one back among the rest by name.
func TestListPinnedMostRecentFirst(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	ids := map[string]string{}
	for _, name := range []string{"Alpha", "Bravo", "Charlie", "Delta"} {
		c, err := store.Create(ctx, "u1", name, "")
		if err != nil {
			t.Fatal(err)
		}
		ids[name] = c.ID
	}
	pin := func(name string, value bool) {
		t.Helper()
		if _, err := store.Update(ctx, ids[name], "u1", collections.Update{Pinned: &value}); err != nil {
			t.Fatal(err)
		}
	}
	names := func() string {
		t.Helper()
		list, err := store.List(ctx, "u1")
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, c := range list {
			out = append(out, c.Name)
		}
		return strings.Join(out, ",")
	}

	pin("Charlie", true)
	pin("Alpha", true)
	pin("Delta", true)
	if got := names(); got != "Delta,Alpha,Charlie,Bravo" {
		t.Errorf("after pins = %s, want Delta,Alpha,Charlie,Bravo", got)
	}
	// Renaming a pinned collection keeps its place; re-pinning one moves it up.
	newName := "Alpha2"
	if _, err := store.Update(ctx, ids["Alpha"], "u1", collections.Update{Name: &newName}); err != nil {
		t.Fatal(err)
	}
	pin("Charlie", false)
	pin("Charlie", true)
	if got := names(); got != "Charlie,Delta,Alpha2,Bravo" {
		t.Errorf("after re-pin = %s, want Charlie,Delta,Alpha2,Bravo", got)
	}
	pin("Delta", false)
	if got := names(); got != "Charlie,Alpha2,Bravo,Delta" {
		t.Errorf("after unpin = %s, want Charlie,Alpha2,Bravo,Delta", got)
	}
}

// TestPinLimitAndHiding: five pins at most; hiding one unpins it, which
// frees a pin.
func TestPinLimitAndHiding(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	yes, no := true, false
	var ids []string
	for i := 0; i < collections.MaxPinned+1; i++ {
		c, err := store.Create(ctx, "u1", string(rune('A'+i)), "")
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, c.ID)
	}
	for _, id := range ids[:collections.MaxPinned] {
		if _, err := store.Update(ctx, id, "u1", collections.Update{Pinned: &yes}); err != nil {
			t.Fatal(err)
		}
	}
	last := ids[collections.MaxPinned]
	if _, err := store.Update(ctx, last, "u1", collections.Update{Pinned: &yes}); !errors.Is(err, collections.ErrTooManyPinned) {
		t.Fatalf("sixth pin: err = %v, want ErrTooManyPinned", err)
	}
	// Re-saving one already pinned is not a new pin.
	if _, err := store.Update(ctx, ids[0], "u1", collections.Update{Pinned: &yes}); err != nil {
		t.Errorf("re-pin: %v", err)
	}
	hidden, err := store.Update(ctx, ids[0], "u1", collections.Update{Hidden: &yes})
	if err != nil || !hidden.Hidden || hidden.Pinned {
		t.Fatalf("hide: %v, %+v; want hidden and unpinned", err, hidden)
	}
	if _, err := store.Update(ctx, last, "u1", collections.Update{Pinned: &yes}); err != nil {
		t.Errorf("pin after a hide freed one: %v", err)
	}
	shown, err := store.Update(ctx, ids[0], "u1", collections.Update{Hidden: &no})
	if err != nil || shown.Hidden {
		t.Errorf("unhide: %v, %+v", err, shown)
	}
}
