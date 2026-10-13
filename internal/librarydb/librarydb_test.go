package librarydb_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/Jishnu-Prasad888/Orchard/internal/librarydb"
)

func TestOpenCreatesAndMigrates(t *testing.T) {
	orchardDir := filepath.Join(t.TempDir(), ".orchard")
	if err := os.MkdirAll(orchardDir, 0o755); err != nil {
		t.Fatal(err)
	}

	pool, err := librarydb.Open(orchardDir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = pool.Close() }()

	// indexed_files table must exist.
	var name string
	if err := pool.QueryRow(
		`SELECT name FROM sqlite_master WHERE type='table' AND name='indexed_files'`,
	).Scan(&name); err != nil {
		t.Fatalf("indexed_files table missing: %v", err)
	}

	// index_jobs table must exist.
	if err := pool.QueryRow(
		`SELECT name FROM sqlite_master WHERE type='table' AND name='index_jobs'`,
	).Scan(&name); err != nil {
		t.Fatalf("index_jobs table missing: %v", err)
	}

	// book_metadata table must exist (internal/bookmeta's extraction pipeline).
	if err := pool.QueryRow(
		`SELECT name FROM sqlite_master WHERE type='table' AND name='book_metadata'`,
	).Scan(&name); err != nil {
		t.Fatalf("book_metadata table missing: %v", err)
	}

	// reading_status, file_opens, reading_progress, collections, and
	// collection_items tables must exist (Phase 1: reading status and
	// recently-opened tracking; Phase 2: reading progress; Phase 3:
	// collections).
	for _, table := range []string{
		"reading_status", "file_opens", "reading_progress", "collections", "collection_items",
		"bookmarks", "highlights", "quotes", "notes", "note_tags", "note_links",
		"fts_content", "fts_content_state", "recent_searches",
	} {
		q := `SELECT name FROM sqlite_master WHERE type='table' AND name='` + table + `'`
		if err := pool.QueryRow(q).Scan(&name); err != nil {
			t.Errorf("table %s missing: %v", table, err)
		}
	}
}

// TestOpenAddsUserEditedToExistingBookMetadata proves a library database
// created before book_metadata.user_edited existed is brought forward on
// open, so the book-details editing UI can rely on the column being there.
func TestOpenAddsUserEditedToExistingBookMetadata(t *testing.T) {
	orchardDir := filepath.Join(t.TempDir(), ".orchard")
	if err := os.MkdirAll(orchardDir, 0o755); err != nil {
		t.Fatal(err)
	}

	old, err := sql.Open("sqlite", filepath.Join(orchardDir, "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = old.Exec(`
		CREATE TABLE indexed_files (
			id            TEXT PRIMARY KEY,
			rel_path      TEXT NOT NULL UNIQUE,
			size_bytes    INTEGER NOT NULL,
			mod_time      TEXT NOT NULL,
			content_hash  TEXT,
			status        TEXT NOT NULL DEFAULT 'present',
			first_seen_at TEXT NOT NULL,
			last_seen_at  TEXT NOT NULL,
			indexed_at    TEXT NOT NULL
		);
		CREATE TABLE book_metadata (
			file_id      TEXT PRIMARY KEY REFERENCES indexed_files(id) ON DELETE CASCADE,
			title        TEXT NOT NULL DEFAULT '',
			author       TEXT NOT NULL DEFAULT '',
			publisher    TEXT NOT NULL DEFAULT '',
			year         TEXT NOT NULL DEFAULT '',
			isbn         TEXT NOT NULL DEFAULT '',
			description  TEXT NOT NULL DEFAULT '',
			cover_path   TEXT NOT NULL DEFAULT '',
			text_path    TEXT NOT NULL DEFAULT '',
			extracted_at TEXT NOT NULL
		);
		INSERT INTO indexed_files VALUES
			('f1', 'book.epub', 10, '2024-01-01T00:00:00Z', NULL, 'present',
			 '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z');
		INSERT INTO book_metadata (file_id, title, extracted_at)
			VALUES ('f1', 'Old Title', '2024-01-01T00:00:00Z');`)
	if err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}

	pool, err := librarydb.Open(orchardDir)
	if err != nil {
		t.Fatalf("Open on a pre-existing library db: %v", err)
	}
	defer func() { _ = pool.Close() }()

	var userEdited int
	var title string
	if err := pool.QueryRow(
		`SELECT user_edited, title FROM book_metadata WHERE file_id = 'f1'`,
	).Scan(&userEdited, &title); err != nil {
		t.Fatalf("book_metadata.user_edited missing after migration: %v", err)
	}
	if userEdited != 0 {
		t.Errorf("user_edited = %d, want 0 for a pre-existing extraction-only row", userEdited)
	}
	if title != "Old Title" {
		t.Errorf("title = %q, want %q (existing data must survive the column add)", title, "Old Title")
	}
}

// TestOpenAddsMediaTypeToExistingLibrary proves a library database created
// before indexed_files.media_type existed is brought forward on open, and that
// the rows already in it are classified rather than left as 'other'.
//
// This is the upgrade path for every library indexed before the column landed.
// A library's database lives inside its own root and is only opened when that
// library is used, so this cannot be a server-side migration: if the column is
// not added here, every ?type= listing fails with "no such column".
func TestOpenAddsMediaTypeToExistingLibrary(t *testing.T) {
	orchardDir := filepath.Join(t.TempDir(), ".orchard")
	if err := os.MkdirAll(orchardDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Build a database in the old shape: the table, minus the media_type column.
	old, err := sql.Open("sqlite", filepath.Join(orchardDir, "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = old.Exec(`
		CREATE TABLE indexed_files (
			id            TEXT PRIMARY KEY,
			rel_path      TEXT NOT NULL UNIQUE,
			size_bytes    INTEGER NOT NULL,
			mod_time      TEXT NOT NULL,
			content_hash  TEXT,
			status        TEXT NOT NULL DEFAULT 'present',
			first_seen_at TEXT NOT NULL,
			last_seen_at  TEXT NOT NULL,
			indexed_at    TEXT NOT NULL
		);
		INSERT INTO indexed_files VALUES
			('f1', 'holiday/beach.jpg', 10, '2024-01-01T00:00:00Z', NULL, 'present',
			 '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z'),
			('f2', 'holiday/clip.mp4', 20, '2024-01-01T00:00:00Z', NULL, 'present',
			 '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z'),
			('f3', 'holiday/noext', 30, '2024-01-01T00:00:00Z', NULL, 'present',
			 '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z');`)
	if err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}

	pool, err := librarydb.Open(orchardDir)
	if err != nil {
		t.Fatalf("Open on a pre-existing library db: %v", err)
	}
	defer func() { _ = pool.Close() }()

	rows, err := pool.Query(`SELECT rel_path, media_type FROM indexed_files ORDER BY rel_path`)
	if err != nil {
		t.Fatalf("media_type column missing after Open: %v", err)
	}
	defer func() { _ = rows.Close() }()

	want := map[string]string{
		"holiday/beach.jpg": "photo",
		"holiday/clip.mp4":  "video",
		"holiday/noext":     "other",
	}
	got := map[string]string{}
	for rows.Next() {
		var path, typ string
		if err := rows.Scan(&path, &typ); err != nil {
			t.Fatal(err)
		}
		got[path] = typ
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for path, wantType := range want {
		if got[path] != wantType {
			t.Errorf("media_type of %s = %q, want %q (all: %v)", path, got[path], wantType, got)
		}
	}

	// Opening again must be a no-op rather than an error: the migration is
	// applied on every open, and the library is opened on every request.
	if _, err := librarydb.Open(orchardDir); err != nil {
		t.Errorf("re-open of a migrated library db: %v", err)
	}
}

// TestOpenAddsUserScopingToExistingFavoritesAndNotes proves a library
// database created before favorites/file_notes had a user_id column is
// brought forward on open: the old library-wide tables are replaced with
// per-user ones, and the new shape actually enforces per-user scoping.
func TestOpenAddsUserScopingToExistingFavoritesAndNotes(t *testing.T) {
	orchardDir := filepath.Join(t.TempDir(), ".orchard")
	if err := os.MkdirAll(orchardDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Build a database in the old shape: file_id-only primary keys, no user_id.
	old, err := sql.Open("sqlite", filepath.Join(orchardDir, "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = old.Exec(`
		CREATE TABLE indexed_files (
			id            TEXT PRIMARY KEY,
			rel_path      TEXT NOT NULL UNIQUE,
			size_bytes    INTEGER NOT NULL,
			mod_time      TEXT NOT NULL,
			content_hash  TEXT,
			status        TEXT NOT NULL DEFAULT 'present',
			first_seen_at TEXT NOT NULL,
			last_seen_at  TEXT NOT NULL,
			indexed_at    TEXT NOT NULL
		);
		CREATE TABLE favorites (
			file_id    TEXT PRIMARY KEY REFERENCES indexed_files(id) ON DELETE CASCADE,
			created_at TEXT NOT NULL
		);
		CREATE TABLE file_notes (
			file_id    TEXT PRIMARY KEY REFERENCES indexed_files(id) ON DELETE CASCADE,
			body       TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL
		);
		INSERT INTO indexed_files VALUES
			('f1', 'book.pdf', 10, '2024-01-01T00:00:00Z', NULL, 'present',
			 '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z');
		INSERT INTO favorites (file_id, created_at) VALUES ('f1', '2024-01-01T00:00:00Z');
		INSERT INTO file_notes (file_id, body, updated_at) VALUES ('f1', 'old note', '2024-01-01T00:00:00Z');`)
	if err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}

	pool, err := librarydb.Open(orchardDir)
	if err != nil {
		t.Fatalf("Open on a pre-existing library db: %v", err)
	}
	defer func() { _ = pool.Close() }()

	for _, table := range []string{"favorites", "file_notes"} {
		var n int
		if err := pool.QueryRow(
			`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = 'user_id'`, table,
		).Scan(&n); err != nil {
			t.Fatalf("check user_id column on %s: %v", table, err)
		}
		if n != 1 {
			t.Errorf("%s.user_id missing after migration", table)
		}
	}

	// The pre-migration row had no owner to attribute it to, so it is
	// dropped rather than guessed at — a fresh per-user row can be added
	// after migration and must round-trip correctly.
	var favCount, noteCount int
	if err := pool.QueryRow(`SELECT COUNT(*) FROM favorites`).Scan(&favCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(`SELECT COUNT(*) FROM file_notes`).Scan(&noteCount); err != nil {
		t.Fatal(err)
	}
	if favCount != 0 || noteCount != 0 {
		t.Errorf("expected legacy unscoped rows to be dropped, got favorites=%d file_notes=%d", favCount, noteCount)
	}

	if _, err := pool.Exec(
		`INSERT INTO favorites (user_id, file_id, created_at) VALUES ('u1', 'f1', '2024-02-01T00:00:00Z')`,
	); err != nil {
		t.Fatalf("insert favorite under new schema: %v", err)
	}
	if _, err := pool.Exec(
		`INSERT INTO file_notes (user_id, file_id, body, updated_at) VALUES ('u1', 'f1', 'new note', '2024-02-01T00:00:00Z')`,
	); err != nil {
		t.Fatalf("insert note under new schema: %v", err)
	}

	// Re-opening (every request opens the library db) must be a no-op, not
	// a repeat drop of the row just inserted.
	pool2, err := librarydb.Open(orchardDir)
	if err != nil {
		t.Fatalf("re-open of a migrated library db: %v", err)
	}
	defer func() { _ = pool2.Close() }()

	if err := pool2.QueryRow(`SELECT COUNT(*) FROM favorites WHERE user_id = 'u1'`).Scan(&favCount); err != nil {
		t.Fatal(err)
	}
	if favCount != 1 {
		t.Errorf("favorite inserted post-migration was lost on re-open, count = %d", favCount)
	}
}

func TestOpenDBWrapper(t *testing.T) {
	orchardDir := filepath.Join(t.TempDir(), ".orchard")
	if err := os.MkdirAll(orchardDir, 0o755); err != nil {
		t.Fatal(err)
	}

	db, err := librarydb.OpenDB(orchardDir)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	if db.DB() == nil {
		t.Error("DB() returned nil")
	}
	if err := db.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestOpenIdempotent(t *testing.T) {
	orchardDir := filepath.Join(t.TempDir(), ".orchard")
	if err := os.MkdirAll(orchardDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Opening the same database twice is idempotent (migrations re-run safely).
	for i := 0; i < 3; i++ {
		pool, err := librarydb.Open(orchardDir)
		if err != nil {
			t.Fatalf("Open #%d: %v", i+1, err)
		}
		_ = pool.Close()
	}
}

// TestFTSContentTriggersSyncQuotesHighlightsNotes proves the AFTER INSERT/
// UPDATE/DELETE triggers on quotes/highlights/notes keep fts_content current
// without any Go-side sync call (unlike book_metadata/book_text, which are
// synced explicitly by internal/bookmeta — see the schema comment on
// fts_content).
func TestFTSContentTriggersSyncQuotesHighlightsNotes(t *testing.T) {
	orchardDir := filepath.Join(t.TempDir(), ".orchard")
	if err := os.MkdirAll(orchardDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pool, err := librarydb.Open(orchardDir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = pool.Close() }()

	now := "2024-01-01T00:00:00Z"
	if _, err := pool.Exec(`INSERT INTO indexed_files
		(id, rel_path, size_bytes, mod_time, status, first_seen_at, last_seen_at, indexed_at)
		VALUES ('f1', 'book.epub', 10, ?, 'present', ?, ?, ?)`, now, now, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(`INSERT INTO quotes
		(id, user_id, file_id, text, commentary, created_at, updated_at)
		VALUES ('q1', 'u1', 'f1', 'fear is the mindkiller', 'a favorite', ?, ?)`, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(`INSERT INTO highlights
		(id, user_id, file_id, location, text, note, created_at, updated_at)
		VALUES ('h1', 'u1', 'f1', 'page:1', 'the spice must flow', '', ?, ?)`, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(`INSERT INTO notes
		(id, user_id, title, body, created_at, updated_at)
		VALUES ('n1', 'u1', 'thoughts', 'a standalone note about dunes', ?, ?)`, now, now); err != nil {
		t.Fatal(err)
	}

	assertMatch := func(needle, wantSourceType, wantSourceID string) {
		t.Helper()
		var sourceType, sourceID string
		err := pool.QueryRow(
			`SELECT source_type, source_id FROM fts_content WHERE fts_content MATCH ?`, needle,
		).Scan(&sourceType, &sourceID)
		if err != nil {
			t.Fatalf("MATCH %q: %v", needle, err)
		}
		if sourceType != wantSourceType || sourceID != wantSourceID {
			t.Errorf("MATCH %q = (%s, %s), want (%s, %s)", needle, sourceType, sourceID, wantSourceType, wantSourceID)
		}
	}
	assertMatch("mindkiller", "quote", "q1")
	assertMatch("spice", "highlight", "h1")
	assertMatch("dunes", "note", "n1")

	// Update: the old text must no longer match, the new text must.
	if _, err := pool.Exec(`UPDATE quotes SET text = 'updated quote text' WHERE id = 'q1'`); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(`SELECT COUNT(*) FROM fts_content WHERE fts_content MATCH 'mindkiller'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("old quote text still indexed after update, count = %d", count)
	}
	assertMatch("updated", "quote", "q1")

	// Delete: the row must be gone from fts_content too.
	if _, err := pool.Exec(`DELETE FROM highlights WHERE id = 'h1'`); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(`SELECT COUNT(*) FROM fts_content WHERE fts_content MATCH 'spice'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("deleted highlight still indexed, count = %d", count)
	}

	// Cascade delete (the file itself, not the quote): quotes.file_id
	// REFERENCES indexed_files ON DELETE CASCADE still fires the quotes
	// DELETE trigger, since SQLite's FK cascade is a real DELETE.
	if _, err := pool.Exec(`DELETE FROM indexed_files WHERE id = 'f1'`); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(`SELECT COUNT(*) FROM fts_content WHERE fts_content MATCH 'quote'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("quote survived its file's cascade delete in fts_content, count = %d", count)
	}
}

// TestOpenBackfillsFTSContent proves a library database created before
// fts_content existed is brought forward on open: pre-existing quotes,
// highlights, notes, book_metadata, and extracted book text (read back from
// disk) all get indexed once, and a second Open() does not duplicate rows.
func TestOpenBackfillsFTSContent(t *testing.T) {
	orchardDir := filepath.Join(t.TempDir(), ".orchard")
	if err := os.MkdirAll(orchardDir, 0o755); err != nil {
		t.Fatal(err)
	}

	textDir := filepath.Join(orchardDir, "text")
	if err := os.MkdirAll(textDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(textDir, "f1.txt"), []byte("the desert planet arrakis"), 0o644); err != nil {
		t.Fatal(err)
	}

	old, err := sql.Open("sqlite", filepath.Join(orchardDir, "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = old.Exec(`
		CREATE TABLE indexed_files (
			id TEXT PRIMARY KEY, rel_path TEXT NOT NULL UNIQUE, size_bytes INTEGER NOT NULL,
			mod_time TEXT NOT NULL, content_hash TEXT, status TEXT NOT NULL DEFAULT 'present',
			first_seen_at TEXT NOT NULL, last_seen_at TEXT NOT NULL, indexed_at TEXT NOT NULL
		);
		CREATE TABLE book_metadata (
			file_id TEXT PRIMARY KEY, title TEXT NOT NULL DEFAULT '', author TEXT NOT NULL DEFAULT '',
			publisher TEXT NOT NULL DEFAULT '', year TEXT NOT NULL DEFAULT '', isbn TEXT NOT NULL DEFAULT '',
			description TEXT NOT NULL DEFAULT '', cover_path TEXT NOT NULL DEFAULT '',
			text_path TEXT NOT NULL DEFAULT '', user_edited INTEGER NOT NULL DEFAULT 0, extracted_at TEXT NOT NULL
		);
		CREATE TABLE quotes (
			id TEXT PRIMARY KEY, user_id TEXT NOT NULL, file_id TEXT NOT NULL, text TEXT NOT NULL,
			location TEXT NOT NULL DEFAULT '', author TEXT NOT NULL DEFAULT '', chapter TEXT NOT NULL DEFAULT '',
			page TEXT NOT NULL DEFAULT '', commentary TEXT NOT NULL DEFAULT '', favorite INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL, updated_at TEXT NOT NULL
		);
		CREATE TABLE highlights (
			id TEXT PRIMARY KEY, user_id TEXT NOT NULL, file_id TEXT NOT NULL, location TEXT NOT NULL,
			text TEXT NOT NULL DEFAULT '', color TEXT NOT NULL DEFAULT 'yellow', note TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL, updated_at TEXT NOT NULL
		);
		CREATE TABLE notes (
			id TEXT PRIMARY KEY, user_id TEXT NOT NULL, file_id TEXT, quote_id TEXT, highlight_id TEXT,
			title TEXT NOT NULL DEFAULT '', body TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL, updated_at TEXT NOT NULL
		);
		INSERT INTO indexed_files VALUES
			('f1', 'book.epub', 10, '2024-01-01T00:00:00Z', NULL, 'present',
			 '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z');
		INSERT INTO book_metadata (file_id, title, author, description, extracted_at)
			VALUES ('f1', 'Dune', 'Frank Herbert', 'a desert planet saga', '2024-01-01T00:00:00Z');
		INSERT INTO quotes (id, user_id, file_id, text, commentary, created_at, updated_at)
			VALUES ('q1', 'u1', 'f1', 'fear is the mindkiller', '', '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z');
		INSERT INTO highlights (id, user_id, file_id, location, text, created_at, updated_at)
			VALUES ('h1', 'u1', 'f1', 'page:1', 'the spice must flow', '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z');
		INSERT INTO notes (id, user_id, title, body, created_at, updated_at)
			VALUES ('n1', 'u1', 'thoughts', 'a standalone note about dunes', '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z');
		UPDATE book_metadata SET text_path = 'text/f1.txt' WHERE file_id = 'f1';`)
	if err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}

	pool, err := librarydb.Open(orchardDir)
	if err != nil {
		t.Fatalf("Open on a pre-existing library db: %v", err)
	}
	defer func() { _ = pool.Close() }()

	assertMatch := func(needle, wantSourceType string) {
		t.Helper()
		var sourceType string
		err := pool.QueryRow(
			`SELECT source_type FROM fts_content WHERE fts_content MATCH ?`, needle,
		).Scan(&sourceType)
		if err != nil {
			t.Fatalf("MATCH %q: %v", needle, err)
		}
		if sourceType != wantSourceType {
			t.Errorf("MATCH %q source_type = %q, want %q", needle, sourceType, wantSourceType)
		}
	}
	assertMatch("mindkiller", "quote")
	assertMatch("spice", "highlight")
	assertMatch("dunes", "note")
	assertMatch("Herbert", "metadata")
	assertMatch("arrakis", "book_text")

	// Re-opening must not duplicate the backfilled rows.
	pool2, err := librarydb.Open(orchardDir)
	if err != nil {
		t.Fatalf("re-open: %v", err)
	}
	defer func() { _ = pool2.Close() }()

	var count int
	if err := pool2.QueryRow(`SELECT COUNT(*) FROM fts_content WHERE fts_content MATCH 'mindkiller'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("backfill duplicated on re-open, count = %d, want 1", count)
	}
}
