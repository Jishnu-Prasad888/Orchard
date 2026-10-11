// Package librarydb manages per-library SQLite databases.
//
// Each Orchard library keeps its own SQLite database at <root>/.orchard/library.db.
// This database holds library-scoped data: indexed files, folders, jobs,
// thumbnails, embeddings, and any other data that travels with the library.
//
// The server-level database (internal/db) is used for global concerns:
// user accounts, sessions, registered libraries, global settings, and audit
// records.
//
// This package imports internal/media for the extension-to-type classifier,
// because a library's database is only ever opened from inside that library's
// own root, so a schema change has to be applied by the code that opens it
// rather than by a server-side migration. The dependency is safe: internal/media
// never imports this package — it is handed an open *sql.DB instead.
package librarydb

import (
	"database/sql"
	"fmt"
	"path/filepath"

	"github.com/Jishnu-Prasad888/Orchard/internal/media"

	_ "modernc.org/sqlite" // pure-Go SQLite driver
)

const (
	// dbFileName is the per-library database filename inside .orchard/.
	dbFileName = "library.db"

	// SchemaVersion is the current version of the library-level database
	// schema. Bump this when adding new tables or changing existing ones.
	SchemaVersion = 1
)

// DB wraps a per-library SQLite connection pool. Use OpenDB to construct one.
type DB struct {
	pool *sql.DB
}

// DB returns the underlying *sql.DB connection pool.
func (d *DB) DB() *sql.DB { return d.pool }

// Close closes the underlying connection pool.
func (d *DB) Close() error { return d.pool.Close() }

// OpenDB opens (creating if necessary) the per-library SQLite database at
// <orchardDir>/library.db and applies any pending schema migrations.
func OpenDB(orchardDir string) (*DB, error) {
	pool, err := Open(orchardDir)
	if err != nil {
		return nil, err
	}
	return &DB{pool: pool}, nil
}

// Open opens (creating if necessary) the per-library SQLite database at
// <orchardDir>/library.db and applies any pending schema migrations.
func Open(orchardDir string) (*sql.DB, error) {
	path := filepath.Join(orchardDir, dbFileName)
	pool, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("open library database: %w", err)
	}
	pool.SetMaxOpenConns(1)
	pool.SetMaxIdleConns(1)
	pool.SetConnMaxLifetime(0)
	pool.SetConnMaxIdleTime(0)

	if err := pool.Ping(); err != nil {
		_ = pool.Close()
		return nil, fmt.Errorf("ping library database: %w", err)
	}
	if _, err := pool.Exec(`PRAGMA journal_mode = WAL`); err != nil {
		_ = pool.Close()
		return nil, fmt.Errorf("enable WAL: %w", err)
	}
	if err := migrate(pool); err != nil {
		_ = pool.Close()
		return nil, fmt.Errorf("migrate library database: %w", err)
	}
	return pool, nil
}

func dsn(path string) string {
	pragmas := []string{
		"foreign_keys(1)",
		"busy_timeout(5000)",
		"synchronous(NORMAL)",
		"journal_mode(WAL)",
	}
	u := "file:" + filepath.ToSlash(path) + "?"
	for i, p := range pragmas {
		if i > 0 {
			u += "&"
		}
		u += "_pragma=" + p
	}
	return u
}

// migrate applies the per-library schema. Each statement is idempotent via
// CREATE TABLE IF NOT EXISTS / CREATE INDEX IF NOT EXISTS.
func migrate(db *sql.DB) error {
	if _, err := db.Exec(schema); err != nil {
		return err
	}
	if err := addIndexedFileMediaType(db); err != nil {
		return err
	}
	return addFavoriteNoteUserScoping(db)
}

// addIndexedFileMediaType backfills indexed_files.media_type on a library
// database created before that column existed.
//
// SQLite has no `ADD COLUMN IF NOT EXISTS`, so a library that was indexed
// before this change would otherwise keep a table without the column and every
// `?type=` listing would fail with "no such column". A library's database
// lives inside its own root, so this cannot be a server-side migration: the
// file is only opened when that library is next used.
func addIndexedFileMediaType(db *sql.DB) error {
	has, err := columnExists(db, "indexed_files", "media_type")
	if err != nil {
		return err
	}
	if !has {
		if _, err := db.Exec(`ALTER TABLE indexed_files
			ADD COLUMN media_type TEXT NOT NULL DEFAULT 'other'
			CHECK (media_type IN ('photo','video','audio','document','other'))`); err != nil {
			return err
		}
	}
	if _, err := db.Exec(
		`CREATE INDEX IF NOT EXISTS indexed_files_type_idx ON indexed_files (media_type)`); err != nil {
		return err
	}
	// Covers the dashboard's per-type counts and newest-first listings.
	if _, err := db.Exec(
		`CREATE INDEX IF NOT EXISTS indexed_files_status_type_mod_idx
		 ON indexed_files (status, media_type, mod_time)`); err != nil {
		return err
	}
	// Every existing row is still classified as 'other' at this point. The
	// extension is what the type is derived from, so the backfill is a rewrite
	// rather than a guess — it is also exactly what a re-index would store, so
	// the two paths cannot disagree.
	rows, err := db.Query(`SELECT id, rel_path FROM indexed_files WHERE media_type = 'other'`)
	if err != nil {
		return err
	}
	type pending struct {
		id, relPath, mediaType string
	}
	var updates []pending
	for rows.Next() {
		var id, relPath string
		if err := rows.Scan(&id, &relPath); err != nil {
			_ = rows.Close()
			return err
		}
		updates = append(updates, pending{id: id, relPath: relPath, mediaType: string(media.DetectMediaType(relPath))})
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, u := range updates {
		if _, err := db.Exec(`UPDATE indexed_files SET media_type = ? WHERE id = ?`, u.mediaType, u.id); err != nil {
			return err
		}
	}
	return nil
}

// addFavoriteNoteUserScoping migrates favorites and file_notes from
// library-wide to per-user records. Both tables originally had no user_id at
// all (file_id was the whole primary key), so a favorite or note was visible
// to every account on the library — Phase 0a of the library roadmap fixes
// this so reading status, progress, bookmarks, and quotes can all follow the
// same per-user pattern from the start.
//
// SQLite can't ALTER a primary key, so a library database created before
// this change is migrated by recreating the table. No Orchard install has
// gone further than the schema-only baseline at this point, so there is no
// real per-user attribution to preserve for any existing rows; they are
// dropped rather than guessed at.
func addFavoriteNoteUserScoping(db *sql.DB) error {
	migrations := []struct {
		table    string
		recreate string
	}{
		{"favorites", `CREATE TABLE favorites (
			user_id    TEXT NOT NULL,
			file_id    TEXT NOT NULL REFERENCES indexed_files(id) ON DELETE CASCADE,
			created_at TEXT NOT NULL,
			PRIMARY KEY (user_id, file_id)
		)`},
		{"file_notes", `CREATE TABLE file_notes (
			user_id    TEXT NOT NULL,
			file_id    TEXT NOT NULL REFERENCES indexed_files(id) ON DELETE CASCADE,
			body       TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL,
			PRIMARY KEY (user_id, file_id)
		)`},
	}
	for _, m := range migrations {
		has, err := columnExists(db, m.table, "user_id")
		if err != nil {
			return err
		}
		if has {
			continue
		}
		if _, err := db.Exec(fmt.Sprintf("DROP TABLE %s", m.table)); err != nil {
			return err
		}
		if _, err := db.Exec(m.recreate); err != nil {
			return err
		}
	}
	// file_id lookups (cascade from indexed_files, collection counts) no
	// longer hit the primary key directly once user_id leads it.
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS favorites_file_idx ON favorites (file_id)`); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS file_notes_file_idx ON file_notes (file_id)`); err != nil {
		return err
	}
	return nil
}

// columnExists reports whether a table already has the named column.
func columnExists(db *sql.DB, table, column string) (bool, error) {
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var (
			cid        int
			name       string
			ctype      string
			notNull    int
			dflt       sql.NullString
			primaryKey int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &primaryKey); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

// schema is the canonical per-library DDL. All tables use CREATE TABLE IF NOT
// EXISTS so the function is safe to call on an already-initialised database.
const schema = `
-- Tracking table for every file the indexer has seen under the library root.
-- Relative paths are relative to the library root (never absolute) so the
-- library remains portable when remounted at a different path.
CREATE TABLE IF NOT EXISTS indexed_files (
	id            TEXT PRIMARY KEY,
	rel_path      TEXT NOT NULL UNIQUE,
	size_bytes    INTEGER NOT NULL,
	mod_time      TEXT NOT NULL,
	content_hash  TEXT,
	media_type    TEXT NOT NULL DEFAULT 'other'
		CHECK (media_type IN ('photo','video','audio','document','other')),
	status        TEXT NOT NULL DEFAULT 'present'
		CHECK (status IN ('present', 'missing', 'deleted')),
	first_seen_at TEXT NOT NULL,
	last_seen_at  TEXT NOT NULL,
	indexed_at    TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS indexed_files_rel_path_idx ON indexed_files (rel_path);
CREATE INDEX IF NOT EXISTS indexed_files_status_idx   ON indexed_files (status);
CREATE INDEX IF NOT EXISTS indexed_files_hash_idx     ON indexed_files (content_hash)
	WHERE content_hash IS NOT NULL;
-- indexed_files_type_idx is deliberately not created here: on a database that
-- predates the media_type column it would fail before the column is added. It
-- is created by addIndexedFileMediaType, which runs for every database.

-- Background job queue for library-scoped operations.
-- Jobs are persisted so they survive process restarts; a job that was
-- 'running' at shutdown is reset to 'queued' on the next startup.
CREATE TABLE IF NOT EXISTS index_jobs (
	id           TEXT PRIMARY KEY,
	kind         TEXT NOT NULL,
	status       TEXT NOT NULL DEFAULT 'queued'
		CHECK (status IN ('queued', 'running', 'completed', 'failed', 'cancelled')),
	priority     INTEGER NOT NULL DEFAULT 0,
	payload      TEXT NOT NULL DEFAULT '{}',
	result       TEXT,
	error_msg    TEXT,
	attempt      INTEGER NOT NULL DEFAULT 0,
	max_attempts INTEGER NOT NULL DEFAULT 3,
	created_at   TEXT NOT NULL,
	started_at   TEXT,
	finished_at  TEXT,
	next_run_at  TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS index_jobs_status_idx      ON index_jobs (status);
CREATE INDEX IF NOT EXISTS index_jobs_next_run_idx    ON index_jobs (next_run_at)
	WHERE status IN ('queued', 'failed');

-- folders tracks directories under the library root that contain indexed files.
CREATE TABLE IF NOT EXISTS folders (
	id           TEXT PRIMARY KEY,
	rel_path     TEXT NOT NULL UNIQUE,
	parent_id    TEXT REFERENCES folders(id),
	name         TEXT NOT NULL,
	file_count   INTEGER NOT NULL DEFAULT 0,
	created_at   TEXT NOT NULL,
	updated_at   TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS folders_rel_path_idx ON folders (rel_path);
CREATE INDEX IF NOT EXISTS folders_parent_idx   ON folders (parent_id);

-- trash records soft-deleted files for restore or permanent deletion.
CREATE TABLE IF NOT EXISTS trash (
	file_id         TEXT PRIMARY KEY REFERENCES indexed_files(id) ON DELETE CASCADE,
	original_path   TEXT NOT NULL,
	trash_path      TEXT NOT NULL,
	deleted_at      TEXT NOT NULL,
	deleted_by      TEXT
);

-- fts_files is a FTS5 virtual table for full-text search over file paths and
-- names. The file_id column is UNINDEXED (stored but not tokenized) so
-- searches only match against rel_path. The default unicode61 tokenizer splits
-- on '/' and '.' so searching "beach" matches "holiday/beach.jpg".
CREATE VIRTUAL TABLE IF NOT EXISTS fts_files USING fts5(
	file_id UNINDEXED,
	rel_path
);

-- Keep fts_files in sync with indexed_files.
CREATE TRIGGER IF NOT EXISTS fts_files_insert AFTER INSERT ON indexed_files BEGIN
	INSERT INTO fts_files(file_id, rel_path) VALUES (new.id, new.rel_path);
END;

CREATE TRIGGER IF NOT EXISTS fts_files_update AFTER UPDATE OF rel_path ON indexed_files BEGIN
	DELETE FROM fts_files WHERE file_id = old.id;
	INSERT INTO fts_files(file_id, rel_path) VALUES (new.id, new.rel_path);
END;

CREATE TRIGGER IF NOT EXISTS fts_files_delete AFTER DELETE ON indexed_files BEGIN
	DELETE FROM fts_files WHERE file_id = old.id;
END;

-- tags holds user-defined labels that can be attached to any indexed file.
CREATE TABLE IF NOT EXISTS tags (
	id         TEXT PRIMARY KEY,
	name       TEXT NOT NULL UNIQUE COLLATE NOCASE,
	color      TEXT,
	created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS tags_name_idx ON tags (name COLLATE NOCASE);

-- file_tags is the many-to-many join between files and tags.
CREATE TABLE IF NOT EXISTS file_tags (
	file_id    TEXT NOT NULL REFERENCES indexed_files(id) ON DELETE CASCADE,
	tag_id     TEXT NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
	created_at TEXT NOT NULL,
	PRIMARY KEY (file_id, tag_id)
);

CREATE INDEX IF NOT EXISTS file_tags_tag_idx  ON file_tags (tag_id);
CREATE INDEX IF NOT EXISTS file_tags_file_idx ON file_tags (file_id);

-- favorites records files a user has starred. user_id is an auth.User.ID
-- from the server-level database (not a foreign key: this per-library
-- database has no access to the users table, which lives elsewhere so the
-- library stays portable across servers).
CREATE TABLE IF NOT EXISTS favorites (
	user_id    TEXT NOT NULL,
	file_id    TEXT NOT NULL REFERENCES indexed_files(id) ON DELETE CASCADE,
	created_at TEXT NOT NULL,
	PRIMARY KEY (user_id, file_id)
);

-- file_notes stores per-file, per-user Markdown notes (captions,
-- descriptions, journal entries) written in the web UI. They are app-level
-- data and live in the library database so they travel with a portable
-- library. Each user has at most one note per file; an empty/missing body
-- means no note.
CREATE TABLE IF NOT EXISTS file_notes (
	user_id    TEXT NOT NULL,
	file_id    TEXT NOT NULL REFERENCES indexed_files(id) ON DELETE CASCADE,
	body       TEXT NOT NULL DEFAULT '',
	updated_at TEXT NOT NULL,
	PRIMARY KEY (user_id, file_id)
);
`
