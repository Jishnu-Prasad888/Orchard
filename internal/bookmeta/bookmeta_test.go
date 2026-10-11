package bookmeta

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Jishnu-Prasad888/Orchard/internal/librarydb"
)

func newTestStore(t *testing.T) (*Store, *sql.DB, func(fileID, relPath string)) {
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

	seed := func(fileID, relPath string) {
		if _, err := db.Exec(`INSERT INTO indexed_files
			(id, rel_path, size_bytes, mod_time, status, first_seen_at, last_seen_at, indexed_at)
			VALUES (?, ?, 1, '', 'present', '', '', '')`, fileID, relPath); err != nil {
			t.Fatalf("seed file: %v", err)
		}
	}
	return NewStore(db), db, seed
}

func TestStore_GetMissingReturnsNotOK(t *testing.T) {
	store, _, seed := newTestStore(t)
	seed("f1", "book.epub")

	_, ok, err := store.Get(context.Background(), "f1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if ok {
		t.Error("expected ok=false for a file with no book_metadata row yet")
	}
}

func TestStore_UpsertAndGetRoundTrip(t *testing.T) {
	store, _, seed := newTestStore(t)
	seed("f1", "book.epub")

	want := Metadata{
		FileID:      "f1",
		Title:       "The Great Test",
		Author:      "Jane Author",
		Publisher:   "Test Press",
		Year:        "2021",
		ISBN:        "978-3-16-148410-0",
		Description: "A book about tests.",
		CoverPath:   "covers/f1.jpg",
		TextPath:    "text/f1.txt",
		ExtractedAt: time.Now().UTC(),
	}
	if err := store.Upsert(context.Background(), want); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, ok, err := store.Get(context.Background(), "f1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true after Upsert")
	}
	if got.Title != want.Title || got.Author != want.Author || got.Publisher != want.Publisher ||
		got.Year != want.Year || got.ISBN != want.ISBN || got.Description != want.Description ||
		got.CoverPath != want.CoverPath || got.TextPath != want.TextPath {
		t.Errorf("round-trip mismatch:\n got  = %+v\n want = %+v", got, want)
	}
	if got.ExtractedAt.IsZero() {
		t.Error("ExtractedAt should be set after round-trip")
	}
}

func TestStore_UpsertOverwritesExistingRow(t *testing.T) {
	store, _, seed := newTestStore(t)
	seed("f1", "book.epub")

	if err := store.Upsert(context.Background(), Metadata{FileID: "f1", Title: "First Pass"}); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if err := store.Upsert(context.Background(), Metadata{FileID: "f1", Title: "Re-extracted"}); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	got, ok, err := store.Get(context.Background(), "f1")
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if got.Title != "Re-extracted" {
		t.Errorf("Title = %q, want %q (second upsert should win)", got.Title, "Re-extracted")
	}
}

// TestBookMetadataCascadesWithFile proves the book_metadata row is removed
// when its indexed_files row is permanently deleted, matching the same
// ON DELETE CASCADE pattern used by favorites and file_notes.
func TestBookMetadataCascadesWithFile(t *testing.T) {
	store, db, seed := newTestStore(t)
	seed("f1", "book.epub")
	if err := store.Upsert(context.Background(), Metadata{FileID: "f1", Title: "Doomed"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	if _, err := db.Exec(`DELETE FROM indexed_files WHERE id = 'f1'`); err != nil {
		t.Fatalf("delete file: %v", err)
	}

	_, ok, err := store.Get(context.Background(), "f1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if ok {
		t.Error("expected book_metadata row to be gone after its file was deleted")
	}
}
