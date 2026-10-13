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

func TestStore_SetUserEdit_CreatesRowWhenNoneExists(t *testing.T) {
	store, _, seed := newTestStore(t)
	seed("f1", "book.epub")

	got, err := store.SetUserEdit(context.Background(), "f1", UserEdit{
		Title:  new("My Title"),
		Author: new("My Author"),
	})
	if err != nil {
		t.Fatalf("SetUserEdit: %v", err)
	}
	if !got.UserEdited {
		t.Error("expected UserEdited = true")
	}
	if got.Title != "My Title" || got.Author != "My Author" {
		t.Errorf("got = %+v", got)
	}

	reloaded, ok, err := store.Get(context.Background(), "f1")
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if !reloaded.UserEdited || reloaded.Title != "My Title" {
		t.Errorf("reloaded = %+v", reloaded)
	}
}

func TestStore_SetUserEdit_PatchesOnlyGivenFields(t *testing.T) {
	store, _, seed := newTestStore(t)
	seed("f1", "book.epub")
	if err := store.Upsert(context.Background(), Metadata{
		FileID: "f1", Title: "Extracted Title", Author: "Extracted Author", Year: "2020",
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := store.SetUserEdit(context.Background(), "f1", UserEdit{Title: new("Corrected Title")})
	if err != nil {
		t.Fatalf("SetUserEdit: %v", err)
	}
	if got.Title != "Corrected Title" {
		t.Errorf("Title = %q, want %q", got.Title, "Corrected Title")
	}
	if got.Author != "Extracted Author" {
		t.Errorf("Author = %q, want unchanged %q", got.Author, "Extracted Author")
	}
	if got.Year != "2020" {
		t.Errorf("Year = %q, want unchanged %q", got.Year, "2020")
	}
}

func TestStore_UpsertDoesNotClobberUserEdit(t *testing.T) {
	store, _, seed := newTestStore(t)
	seed("f1", "book.epub")

	if _, err := store.SetUserEdit(context.Background(), "f1", UserEdit{Title: new("My Title")}); err != nil {
		t.Fatalf("SetUserEdit: %v", err)
	}

	// A re-scan re-extracts and calls Upsert again — this must not overwrite
	// the title the person already corrected.
	if err := store.Upsert(context.Background(), Metadata{FileID: "f1", Title: "Re-extracted Title"}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, ok, err := store.Get(context.Background(), "f1")
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if got.Title != "My Title" {
		t.Errorf("Title = %q, want %q (user edit must survive re-extraction)", got.Title, "My Title")
	}
	if !got.UserEdited {
		t.Error("UserEdited should remain true")
	}
}

func TestStore_SetCover(t *testing.T) {
	store, _, seed := newTestStore(t)
	seed("f1", "book.epub")
	if err := store.Upsert(context.Background(), Metadata{FileID: "f1", Title: "A Book", CoverPath: "covers/f1.jpg"}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := store.SetCover(context.Background(), "f1", "covers/f1-custom.png")
	if err != nil {
		t.Fatalf("SetCover: %v", err)
	}
	if got.CoverPath != "covers/f1-custom.png" {
		t.Errorf("CoverPath = %q, want the custom path", got.CoverPath)
	}
	if got.Title != "A Book" {
		t.Errorf("Title = %q, want unchanged %q", got.Title, "A Book")
	}
	if !got.UserEdited {
		t.Error("expected UserEdited = true")
	}

	// A later re-extraction must not revert the custom cover either.
	if err := store.Upsert(context.Background(), Metadata{FileID: "f1", Title: "A Book", CoverPath: "covers/f1.jpg"}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	reloaded, _, err := store.Get(context.Background(), "f1")
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.CoverPath != "covers/f1-custom.png" {
		t.Errorf("CoverPath after re-extraction = %q, want the custom path preserved", reloaded.CoverPath)
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

// TestUpsertSyncsFTSContent proves Upsert keeps fts_content's 'metadata' row
// current — book_metadata is written via UPSERT, so this is done explicitly
// (syncMetadataFTS) rather than left to a trigger.
func TestUpsertSyncsFTSContent(t *testing.T) {
	store, db, seed := newTestStore(t)
	seed("f1", "book.epub")
	ctx := context.Background()

	if err := store.Upsert(ctx, Metadata{FileID: "f1", Title: "Dune", Author: "Frank Herbert"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	var sourceType string
	if err := db.QueryRow(`SELECT source_type FROM fts_content WHERE fts_content MATCH 'Herbert'`).Scan(&sourceType); err != nil {
		t.Fatalf("expected Upsert to index metadata into fts_content: %v", err)
	}
	if sourceType != "metadata" {
		t.Errorf("source_type = %q, want metadata", sourceType)
	}

	// Re-extraction with different author must replace, not duplicate.
	if err := store.Upsert(ctx, Metadata{FileID: "f1", Title: "Dune", Author: "F. Herbert"}); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM fts_content WHERE fts_content MATCH 'Herbert'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("fts_content has %d rows matching Herbert after re-upsert, want 1", count)
	}
}

// TestUpsertAfterUserEditDoesNotStompFTSContent proves that once a user has
// edited a file's metadata, a later re-extraction's Upsert (which the SQL
// WHERE clause already turns into a no-op against book_metadata itself)
// does not overwrite fts_content with the stale extracted values either —
// syncMetadataFTS re-reads the stored row rather than trusting its input.
func TestUpsertAfterUserEditDoesNotStompFTSContent(t *testing.T) {
	store, db, seed := newTestStore(t)
	seed("f1", "book.epub")
	ctx := context.Background()

	userTitle := "My Own Title"
	if _, err := store.SetUserEdit(ctx, "f1", UserEdit{Title: &userTitle}); err != nil {
		t.Fatalf("SetUserEdit: %v", err)
	}

	// A re-scan tries to overwrite with extracted values; book_metadata's
	// own UPDATE is skipped (user_edited = 1), and fts_content must reflect
	// what is actually stored (the user's title), not the extractor's.
	if err := store.Upsert(ctx, Metadata{FileID: "f1", Title: "Extracted Title"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM fts_content WHERE fts_content MATCH 'Extracted'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("fts_content was overwritten with the extractor's title despite a user edit, count = %d", count)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM fts_content WHERE fts_content MATCH 'Own'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("fts_content does not reflect the user-edited title, count = %d", count)
	}
}

// TestSetUserEditSyncsFTSContent proves editing book metadata from the
// details panel updates fts_content too, not only book_metadata.
func TestSetUserEditSyncsFTSContent(t *testing.T) {
	store, db, seed := newTestStore(t)
	seed("f1", "book.epub")
	ctx := context.Background()

	title := "Edited Title"
	if _, err := store.SetUserEdit(ctx, "f1", UserEdit{Title: &title}); err != nil {
		t.Fatalf("SetUserEdit: %v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM fts_content WHERE fts_content MATCH 'Edited'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("fts_content not updated by SetUserEdit, count = %d", count)
	}
}
