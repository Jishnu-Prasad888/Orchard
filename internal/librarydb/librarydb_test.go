package librarydb_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/Jishnu-Prasad888/Cairn/internal/librarydb"
)

func TestOpenCreatesAndMigrates(t *testing.T) {
	cairnDir := filepath.Join(t.TempDir(), ".cairn")
	if err := os.MkdirAll(cairnDir, 0o755); err != nil {
		t.Fatal(err)
	}

	pool, err := librarydb.Open(cairnDir)
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
	cairnDir := filepath.Join(t.TempDir(), ".cairn")
	if err := os.MkdirAll(cairnDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Build a database in the old shape: the table, minus the media_type column.
	old, err := sql.Open("sqlite", filepath.Join(cairnDir, "library.db"))
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

	pool, err := librarydb.Open(cairnDir)
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
	if _, err := librarydb.Open(cairnDir); err != nil {
		t.Errorf("re-open of a migrated library db: %v", err)
	}
}

func TestOpenDBWrapper(t *testing.T) {
	cairnDir := filepath.Join(t.TempDir(), ".cairn")
	if err := os.MkdirAll(cairnDir, 0o755); err != nil {
		t.Fatal(err)
	}

	db, err := librarydb.OpenDB(cairnDir)
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
	cairnDir := filepath.Join(t.TempDir(), ".cairn")
	if err := os.MkdirAll(cairnDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Opening the same database twice is idempotent (migrations re-run safely).
	for i := 0; i < 3; i++ {
		pool, err := librarydb.Open(cairnDir)
		if err != nil {
			t.Fatalf("Open #%d: %v", i+1, err)
		}
		_ = pool.Close()
	}
}
