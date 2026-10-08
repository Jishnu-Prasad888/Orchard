package notes

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Jishnu-Prasad888/Cairn/internal/librarydb"
)

func openTestStore(t *testing.T) *NoteStore {
	t.Helper()
	dir := t.TempDir()
	cairnDir := filepath.Join(dir, ".cairn")
	if err := os.MkdirAll(cairnDir, 0o755); err != nil {
		t.Fatalf("mkdir .cairn: %v", err)
	}
	db, err := librarydb.Open(cairnDir)
	if err != nil {
		t.Fatalf("open library db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewNoteStore(db, "lib1")
}

func seedFile(t *testing.T, store *NoteStore, id string) {
	t.Helper()
	if _, err := store.db.Exec(`INSERT INTO indexed_files
		(id, rel_path, size_bytes, mod_time, status, first_seen_at, last_seen_at, indexed_at)
		VALUES (?, ?, 1, '', 'present', '', '', '')`, id, id+".jpg"); err != nil {
		t.Fatalf("seed file: %v", err)
	}
}

func TestGet_MissingNoteReturnsEmpty(t *testing.T) {
	store := openTestStore(t)
	seedFile(t, store, "f1")

	n, err := store.Get(context.Background(), "f1")
	if err != nil {
		t.Fatalf("get missing note: %v", err)
	}
	if n.FileID != "f1" {
		t.Errorf("FileID = %q, want f1", n.FileID)
	}
	if n.Body != "" {
		t.Errorf("Body = %q, want empty", n.Body)
	}
	if !n.UpdatedAt.IsZero() {
		t.Errorf("UpdatedAt = %v, want zero", n.UpdatedAt)
	}
}

func TestSetAndGetRoundTrip(t *testing.T) {
	store := openTestStore(t)
	seedFile(t, store, "f1")

	body := "# Beach day\n\nSunset over the dunes."
	saved, err := store.Set(context.Background(), "f1", body)
	if err != nil {
		t.Fatalf("set note: %v", err)
	}
	if saved.Body != body {
		t.Errorf("saved body = %q, want %q", saved.Body, body)
	}
	if saved.UpdatedAt.IsZero() {
		t.Error("updated_at should be set")
	}

	n, err := store.Get(context.Background(), "f1")
	if err != nil {
		t.Fatalf("get note: %v", err)
	}
	if n.Body != body {
		t.Errorf("round-trip body = %q, want %q", n.Body, body)
	}
	if n.UpdatedAt.IsZero() {
		t.Error("round-trip updated_at should be set")
	}
}

func TestSet_UpsertsExistingNote(t *testing.T) {
	store := openTestStore(t)
	seedFile(t, store, "f1")

	if _, err := store.Set(context.Background(), "f1", "first"); err != nil {
		t.Fatalf("set first: %v", err)
	}
	first, err := store.Get(context.Background(), "f1")
	if err != nil {
		t.Fatalf("get first: %v", err)
	}
	time.Sleep(2 * time.Millisecond)

	saved, err := store.Set(context.Background(), "f1", "second")
	if err != nil {
		t.Fatalf("set second: %v", err)
	}
	if saved.Body != "second" {
		t.Errorf("body = %q, want second", saved.Body)
	}
	if !saved.UpdatedAt.After(first.UpdatedAt) {
		t.Errorf("updated_at did not advance: %v after %v", saved.UpdatedAt, first.UpdatedAt)
	}

	n, err := store.Get(context.Background(), "f1")
	if err != nil {
		t.Fatalf("get after upsert: %v", err)
	}
	if n.Body != "second" {
		t.Errorf("body after upsert = %q, want second", n.Body)
	}
}

func TestClearRemovesNote(t *testing.T) {
	store := openTestStore(t)
	seedFile(t, store, "f1")

	if _, err := store.Set(context.Background(), "f1", "hello"); err != nil {
		t.Fatalf("set note: %v", err)
	}
	if err := store.Clear(context.Background(), "f1"); err != nil {
		t.Fatalf("clear note: %v", err)
	}
	n, err := store.Get(context.Background(), "f1")
	if err != nil {
		t.Fatalf("get after clear: %v", err)
	}
	if n.Body != "" {
		t.Errorf("body after clear = %q, want empty", n.Body)
	}
}

func TestClear_MissingNoteIsNotAnError(t *testing.T) {
	store := openTestStore(t)
	seedFile(t, store, "f1")

	if err := store.Clear(context.Background(), "f1"); err != nil {
		t.Errorf("clear missing note: %v", err)
	}
}
