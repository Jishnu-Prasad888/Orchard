package memories_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Jishnu-Prasad888/Cairn/internal/librarydb"
	"github.com/Jishnu-Prasad888/Cairn/internal/memories"
)

func newTestStore(t *testing.T) *memories.MemoryStore {
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
	return memories.NewMemoryStore(db)
}

func ctx(t *testing.T) context.Context {
	t.Helper()
	return context.Background()
}

func TestCreateGet(t *testing.T) {
	store := newTestStore(t)
	date := time.Date(2023, 7, 1, 12, 0, 0, 0, time.UTC)

	m, err := store.Create(ctx(t), memories.CreateParams{
		Title:      "Our Rome trip",
		Body:       "We walked the [[media:file1|Colosseum shot]] and ate gelato.",
		MemoryDate: &date,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if m.ID == "" {
		t.Error("ID is empty")
	}
	if m.Title != "Our Rome trip" {
		t.Errorf("Title = %q", m.Title)
	}
	if m.MemoryDate == nil || !m.MemoryDate.Equal(date) {
		t.Errorf("MemoryDate = %v, want %v", m.MemoryDate, date)
	}

	got, err := store.Get(ctx(t), m.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Body != m.Body {
		t.Errorf("Body mismatch")
	}

	refs, err := store.ListRefs(ctx(t), m.ID)
	if err != nil {
		t.Fatalf("ListRefs: %v", err)
	}
	if len(refs) != 1 || string(refs[0].Type) != "media" || refs[0].ID != "file1" {
		t.Errorf("refs = %+v, want one media:file1 ref", refs)
	}
}

func TestCreateEmptyTitleError(t *testing.T) {
	store := newTestStore(t)
	_, err := store.Create(ctx(t), memories.CreateParams{Title: "  "})
	if err == nil {
		t.Error("expected error for blank title")
	}
}

func TestGetNotFound(t *testing.T) {
	store := newTestStore(t)
	_, err := store.Get(ctx(t), "ghost")
	if !errors.Is(err, memories.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestUpdateVersions(t *testing.T) {
	store := newTestStore(t)
	m, err := store.Create(ctx(t), memories.CreateParams{Title: "v1", Body: "first"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	updated, err := store.Update(ctx(t), m.ID, memories.UpdateParams{Title: "v2", Body: "second"})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Title != "v2" || updated.Body != "second" {
		t.Errorf("updated = %+v", updated)
	}

	versions, err := store.ListVersions(ctx(t), m.ID)
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("versions = %d, want 2", len(versions))
	}
	// Newest first; v1 was the first saved revision.
	if versions[0].Version != 2 || versions[0].Body != "second" {
		t.Errorf("versions[0] = %+v, want v2/second", versions[0])
	}
	if versions[1].Version != 1 || versions[1].Body != "first" {
		t.Errorf("versions[1] = %+v, want v1/first", versions[1])
	}

	old, err := store.GetVersion(ctx(t), m.ID, 1)
	if err != nil {
		t.Fatalf("GetVersion: %v", err)
	}
	if old.Title != "v1" || old.Body != "first" {
		t.Errorf("old version = %+v", old)
	}
}

func TestUpdateRefsRewritten(t *testing.T) {
	store := newTestStore(t)
	m, err := store.Create(ctx(t), memories.CreateParams{Title: "t", Body: "[[media:a]]"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.Update(ctx(t), m.ID, memories.UpdateParams{Title: "t", Body: "[[memory:x]] [[tag:y]]"}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	refs, _ := store.ListRefs(ctx(t), m.ID)
	if len(refs) != 2 {
		t.Fatalf("refs = %d, want 2 (memory + tag)", len(refs))
	}
}

func TestUpdateNotFound(t *testing.T) {
	store := newTestStore(t)
	_, err := store.Update(ctx(t), "ghost", memories.UpdateParams{Title: "nope", Body: ""})
	if !errors.Is(err, memories.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestDeleteSoftAndRestore(t *testing.T) {
	store := newTestStore(t)
	m, _ := store.Create(ctx(t), memories.CreateParams{Title: "t", Body: "b"})

	if err := store.Delete(ctx(t), m.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	got, err := store.Get(ctx(t), m.ID)
	if err != nil {
		t.Fatalf("Get after delete: %v", err)
	}
	if !got.Deleted {
		t.Error("expected Deleted=true after soft delete")
	}

	list, _, err := store.List(ctx(t), "", 50)
	if err != nil {
		t.Fatalf("List after delete: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("List after delete = %d, want 0", len(list))
	}

	if err := store.Restore(ctx(t), m.ID); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	got2, _ := store.Get(ctx(t), m.ID)
	if got2.Deleted {
		t.Error("expected Deleted=false after restore")
	}
	list2, _, _ := store.List(ctx(t), "", 50)
	if len(list2) != 1 {
		t.Errorf("List after restore = %d, want 1", len(list2))
	}
}

func TestDeleteNotFound(t *testing.T) {
	store := newTestStore(t)
	if err := store.Delete(ctx(t), "ghost"); !errors.Is(err, memories.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestListPagination(t *testing.T) {
	store := newTestStore(t)
	// Create 5 memories; because IDs are random, ordering is purely by
	// updated_at and then id, so count what we actually get back.
	for i := 0; i < 5; i++ {
		if _, err := store.Create(ctx(t), memories.CreateParams{Title: "m"}); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	var collected []string
	cursor := ""
	for {
		page, next, err := store.List(ctx(t), cursor, 2)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(page) == 0 {
			break
		}
		for _, m := range page {
			if containsID(collected, m.ID) {
				t.Fatalf("duplicate id %s across pages", m.ID)
			}
			collected = append(collected, m.ID)
		}
		if next == "" {
			break
		}
		cursor = next
	}
	if len(collected) != 5 {
		t.Errorf("collected %d memories, want 5", len(collected))
	}
}

func containsID(ids []string, id string) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

func TestSearchMemories(t *testing.T) {
	store := newTestStore(t)
	m, err := store.Create(ctx(t), memories.CreateParams{
		Title: "Beach day",
		Body:  "We went to the sandy cove in June.",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.Create(ctx(t), memories.CreateParams{Title: "City trip", Body: "Museums."}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	results, _, err := store.Search(ctx(t), "sandy", "", 50)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 || results[0].ID != m.ID {
		t.Errorf("search results = %+v, want the beach memory", results)
	}

	empty, _, err := store.Search(ctx(t), "zzzznope", "", 50)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("expected no results for nonsense query, got %d", len(empty))
	}
}

func TestSearchFiltersDeleted(t *testing.T) {
	store := newTestStore(t)
	m, _ := store.Create(ctx(t), memories.CreateParams{Title: "gone", Body: "uniqueword"})
	if err := store.Delete(ctx(t), m.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	results, _, err := store.Search(ctx(t), "uniqueword", "", 50)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected deleted memory to be excluded from search, got %d", len(results))
	}
}
