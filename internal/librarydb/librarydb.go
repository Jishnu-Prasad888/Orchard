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
	"os"
	"path/filepath"

	"github.com/Jishnu-Prasad888/Orchard/internal/media"

	_ "modernc.org/sqlite" // pure-Go SQLite driver
)

const (
	// dbFileName is the per-library database filename inside .orchard/.
	dbFileName = "library.db"

	// SchemaVersion is the current version of the library-level database
	// schema. Bump this when adding new tables or changing existing ones.
	SchemaVersion = 5
)

// DB wraps a per-library SQLite connection pool. Use OpenDB to construct one.
type DB struct {
	pool    *sql.DB
	release func()
}

// DB returns the underlying *sql.DB connection pool.
func (d *DB) DB() *sql.DB { return d.pool }

// Close releases the pool; the shared pool behind it stays open for others.
func (d *DB) Close() error {
	d.release()
	return nil
}

// OpenDB opens (creating if necessary) the per-library SQLite database at
// <orchardDir>/library.db and applies any pending schema migrations.
//
// The pool is shared with every other user of the same library (see
// Acquire); Close releases this handle rather than closing it.
func OpenDB(orchardDir string) (*DB, error) {
	pool, release, err := Acquire(orchardDir)
	if err != nil {
		return nil, err
	}
	return &DB{pool: pool, release: release}, nil
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
	if err := migrate(pool, orchardDir); err != nil {
		_ = pool.Close()
		return nil, fmt.Errorf("migrate library database: %w", err)
	}
	Optimize(pool)
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
// CREATE TABLE IF NOT EXISTS / CREATE INDEX IF NOT EXISTS. orchardDir is
// needed only by addFTSContent, which reads extracted-text files from disk
// to backfill a database created before fts_content existed.
func migrate(db *sql.DB, orchardDir string) error {
	if _, err := db.Exec(schema); err != nil {
		return err
	}
	if err := addIndexedFileMediaType(db); err != nil {
		return err
	}
	if err := addFavoriteNoteUserScoping(db); err != nil {
		return err
	}
	if err := addBookMetadataUserEdited(db); err != nil {
		return err
	}
	if err := addBookMetadataCoverVersion(db); err != nil {
		return err
	}
	if err := addReadingAndOpensIndexes(db); err != nil {
		return err
	}
	if err := addFTSContent(db, orchardDir); err != nil {
		return err
	}
	if err := removeNotesFromSearch(db); err != nil {
		return err
	}
	if err := dropRedundantIndexes(db); err != nil {
		return err
	}
	if err := addFolderIndexes(db); err != nil {
		return err
	}
	return addThumbnails(db)
}

// addThumbnails creates the record of photo thumbnails (internal/thumbs).
// path is relative to .orchard and empty for a photo that could not be
// shrunk; version is the source's mod_time:size when it was rendered.
func addThumbnails(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS thumbnails (
		file_id      TEXT PRIMARY KEY REFERENCES indexed_files(id) ON DELETE CASCADE,
		path         TEXT NOT NULL DEFAULT '',
		version      TEXT NOT NULL,
		generated_at TEXT NOT NULL
	)`)
	return err
}

// Optimize refreshes the query planner's statistics where they are stale,
// as SQLite recommends for long-lived connections and after bulk changes
// (a scan). analysis_limit keeps it to a bounded sample on a big library,
// so it stays cheap on a Raspberry Pi. Failures only cost plan quality.
func Optimize(db *sql.DB) {
	_, _ = db.Exec(`PRAGMA analysis_limit = 400`)
	_, _ = db.Exec(`PRAGMA optimize = 0x10002`)
}

// addFolderIndexes makes folder listings index lookups. parent_dir is a
// virtual generated column — computed from rel_path, so it costs no storage
// and can never disagree with it — holding everything up to and including
// the last slash ("2024/trip/" for "2024/trip/a.jpg", "" at the root).
// Listing a folder was a full-table LIKE scan, because SQLite cannot use an
// index for a case-insensitive LIKE. The (status, mod_time) index serves the
// library-wide newest-first listing Home shows.
func addFolderIndexes(db *sql.DB) error {
	has, err := hiddenColumnExists(db, "indexed_files", "parent_dir")
	if err != nil {
		return err
	}
	if !has {
		if _, err := db.Exec(`ALTER TABLE indexed_files ADD COLUMN parent_dir TEXT
			GENERATED ALWAYS AS (rtrim(rel_path, replace(rel_path, '/', ''))) VIRTUAL`); err != nil {
			return err
		}
	}
	for _, stmt := range []string{
		`CREATE INDEX IF NOT EXISTS indexed_files_parent_idx ON indexed_files (parent_dir, status)`,
		`CREATE INDEX IF NOT EXISTS indexed_files_status_mod_idx ON indexed_files (status, mod_time)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

// hiddenColumnExists is columnExists for columns PRAGMA table_info hides,
// such as generated columns.
func hiddenColumnExists(db *sql.DB, table, column string) (bool, error) {
	var n int
	err := db.QueryRow(
		fmt.Sprintf(`SELECT COUNT(*) FROM pragma_table_xinfo('%s') WHERE name = ?`, table), column).Scan(&n)
	return n > 0, err
}

// dropRedundantIndexes removes indexes that duplicate others: rel_path is
// already indexed by its UNIQUE constraint, and status is the leading column
// of indexed_files_status_type_mod_idx. Each extra index is one more b-tree
// write on every scanner insert and update.
func dropRedundantIndexes(db *sql.DB) error {
	for _, name := range []string{"indexed_files_rel_path_idx", "indexed_files_status_idx"} {
		if _, err := db.Exec(`DROP INDEX IF EXISTS ` + name); err != nil {
			return err
		}
	}
	return nil
}

// removeNotesFromSearch takes the retired Notes feature out of content
// search on a library database that had it: its sync triggers and its
// indexed rows. Both are derived data. The notes, note_tags, note_links, and
// file_notes tables themselves are left as they are — they hold people's own
// writing, which is not deleted just because the app stopped showing it.
func removeNotesFromSearch(db *sql.DB) error {
	for _, trigger := range []string{
		"fts_content_notes_insert", "fts_content_notes_update", "fts_content_notes_delete",
	} {
		if _, err := db.Exec(`DROP TRIGGER IF EXISTS ` + trigger); err != nil {
			return err
		}
	}
	_, err := db.Exec(`DELETE FROM fts_content WHERE source_type = 'note'`)
	return err
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

// addFavoriteNoteUserScoping migrates favorites from library-wide to
// per-user records. The table originally had no user_id at all (file_id
// was the whole primary key), so a favorite was visible
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
	return nil
}

// addBookMetadataUserEdited backfills book_metadata.user_edited on a
// library database created before that column existed. Every existing row
// predates the book-details editing UI, so it defaults to 0 (extraction may
// keep updating it) exactly as ALTER TABLE's DEFAULT already gives it.
func addBookMetadataUserEdited(db *sql.DB) error {
	has, err := columnExists(db, "book_metadata", "user_edited")
	if err != nil {
		return err
	}
	if has {
		return nil
	}
	_, err = db.Exec(`ALTER TABLE book_metadata ADD COLUMN user_edited INTEGER NOT NULL DEFAULT 0`)
	return err
}

// addBookMetadataCoverVersion backfills book_metadata.cover_version on a
// library database created before that column existed. Existing rows get 0,
// which is older than any extractor version — exactly what marks their
// missing covers as worth another attempt.
func addBookMetadataCoverVersion(db *sql.DB) error {
	has, err := columnExists(db, "book_metadata", "cover_version")
	if err != nil {
		return err
	}
	if has {
		return nil
	}
	_, err = db.Exec(`ALTER TABLE book_metadata ADD COLUMN cover_version INTEGER NOT NULL DEFAULT 0`)
	return err
}

// addReadingAndOpensIndexes creates the lookup indexes for reading_status,
// file_opens, and reading_progress. All three are new (created by the
// schema constant on every install), so this never has a missing-table
// edge case to handle — it is kept as its own idempotent step only to
// match the favorites/file_notes pattern above of separating index
// creation from table creation.
func addReadingAndOpensIndexes(db *sql.DB) error {
	stmts := []string{
		`CREATE INDEX IF NOT EXISTS reading_status_file_idx ON reading_status (file_id)`,
		`CREATE INDEX IF NOT EXISTS file_opens_user_time_idx ON file_opens (user_id, opened_at)`,
		`CREATE INDEX IF NOT EXISTS reading_progress_file_idx ON reading_progress (file_id)`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

// addFTSContent backfills fts_content on a library database created before
// it existed: book_metadata, quotes, highlights, and notes rows written
// before the triggers/sync calls that keep fts_content current were in
// place. Book text is backfilled too, read back from the .orchard/text/
// files that extraction already writes (book_metadata.text_path) — that
// content has no DB row of its own to trigger off of. Going forward, quotes/
// highlights/notes are kept in sync by triggers (schema const below);
// book_metadata and book text are synced explicitly by internal/bookmeta
// (an UPSERT's trigger-firing semantics are not something to depend on, and
// book text has no row to trigger off of at all).
//
// fts_content_state is a one-row-per-key marker table existing solely to
// make this backfill cheap to skip on every subsequent Open() — a full
// resync on every open (migrate runs per-Open, and Open is called per
// request by several handlers) would be wasted work once already done.
func addFTSContent(db *sql.DB, orchardDir string) error {
	var done string
	err := db.QueryRow(`SELECT value FROM fts_content_state WHERE key = 'backfilled'`).Scan(&done)
	if err == nil {
		return nil
	}
	if err != sql.ErrNoRows {
		return err
	}

	backfillRows := []struct {
		sourceType string
		query      string
	}{
		{"metadata", `SELECT file_id, file_id, '', trim(title || ' ' || author || ' ' || description)
			FROM book_metadata WHERE trim(title || ' ' || author || ' ' || description) != ''`},
		{"quote", `SELECT id, file_id, user_id, trim(text || ' ' || commentary) FROM quotes`},
		{"highlight", `SELECT id, file_id, user_id, trim(text || ' ' || note) FROM highlights`},
	}
	for _, b := range backfillRows {
		rows, err := db.Query(b.query)
		if err != nil {
			return fmt.Errorf("backfill fts_content (%s): %w", b.sourceType, err)
		}
		type row struct{ sourceID, fileID, userID, text string }
		var pending []row
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.sourceID, &r.fileID, &r.userID, &r.text); err != nil {
				_ = rows.Close()
				return err
			}
			pending = append(pending, r)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		_ = rows.Close()
		for _, r := range pending {
			if _, err := db.Exec(
				`INSERT INTO fts_content (source_type, source_id, file_id, user_id, text) VALUES (?, ?, ?, ?, ?)`,
				b.sourceType, r.sourceID, r.fileID, r.userID, r.text); err != nil {
				return fmt.Errorf("backfill fts_content (%s): %w", b.sourceType, err)
			}
		}
	}

	// Book text: read back what extraction already wrote to disk.
	textRows, err := db.Query(`SELECT file_id, text_path FROM book_metadata WHERE text_path != ''`)
	if err != nil {
		return fmt.Errorf("backfill fts_content (book_text): %w", err)
	}
	type textRow struct{ fileID, textPath string }
	var pendingText []textRow
	for textRows.Next() {
		var r textRow
		if err := textRows.Scan(&r.fileID, &r.textPath); err != nil {
			_ = textRows.Close()
			return err
		}
		pendingText = append(pendingText, r)
	}
	if err := textRows.Err(); err != nil {
		_ = textRows.Close()
		return err
	}
	_ = textRows.Close()
	for _, r := range pendingText {
		data, err := os.ReadFile(filepath.Join(orchardDir, r.textPath))
		if err != nil {
			continue // file missing/unreadable: skip, not fatal to the migration.
		}
		if _, err := db.Exec(
			`INSERT INTO fts_content (source_type, source_id, file_id, user_id, text) VALUES ('book_text', ?, ?, '', ?)`,
			r.fileID, r.fileID, string(data)); err != nil {
			return fmt.Errorf("backfill fts_content (book_text): %w", err)
		}
	}

	_, err = db.Exec(`INSERT INTO fts_content_state (key, value) VALUES ('backfilled', '1')`)
	return err
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

-- book_metadata holds book-style metadata for document files (title,
-- author, cover, extracted text), one row per file, populated by
-- internal/bookmeta's MediaProcessor as documents are indexed. It is
-- library-wide, not per-user: this is bibliographic data about the file
-- itself (like EXIF for a photo), not a personal annotation.
-- user_edited marks a row a person has hand-edited (title, cover, etc. from
-- the book details panel): once set, a later re-extraction (the file was
-- replaced on disk) must never silently overwrite what they typed.
-- cover_version is the version of the cover extractor that last tried this
-- file (see bookmeta.CoverExtractorVersion): a file with no cover and an
-- older version is retried after an upgrade that can now produce one.
CREATE TABLE IF NOT EXISTS book_metadata (
	file_id      TEXT PRIMARY KEY REFERENCES indexed_files(id) ON DELETE CASCADE,
	title        TEXT NOT NULL DEFAULT '',
	author       TEXT NOT NULL DEFAULT '',
	publisher    TEXT NOT NULL DEFAULT '',
	year         TEXT NOT NULL DEFAULT '',
	isbn         TEXT NOT NULL DEFAULT '',
	description  TEXT NOT NULL DEFAULT '',
	cover_path   TEXT NOT NULL DEFAULT '',
	text_path    TEXT NOT NULL DEFAULT '',
	user_edited  INTEGER NOT NULL DEFAULT 0,
	extracted_at TEXT NOT NULL,
	cover_version INTEGER NOT NULL DEFAULT 0
);

-- reading_status is a user's progress through a book (want to read /
-- reading / completed / on hold). Per-user, following the favorites/
-- file_notes pattern: no row means no status set.
CREATE TABLE IF NOT EXISTS reading_status (
	user_id    TEXT NOT NULL,
	file_id    TEXT NOT NULL REFERENCES indexed_files(id) ON DELETE CASCADE,
	status     TEXT NOT NULL CHECK (status IN ('want_to_read', 'reading', 'completed', 'on_hold')),
	updated_at TEXT NOT NULL,
	PRIMARY KEY (user_id, file_id)
);

-- file_opens records, per user, the last time a file was opened in the
-- viewer. One row per (user, file) — this is "last opened", not a full
-- history log — so "recently opened" is a single indexed query.
CREATE TABLE IF NOT EXISTS file_opens (
	user_id    TEXT NOT NULL,
	file_id    TEXT NOT NULL REFERENCES indexed_files(id) ON DELETE CASCADE,
	opened_at  TEXT NOT NULL,
	PRIMARY KEY (user_id, file_id)
);

-- reading_progress is a user's resume point in a book. location is opaque
-- to the server — a page number for PDF, a CFI string for EPUB, a scroll
-- fraction for Markdown/text — meaningful only to the reader that wrote it.
-- percent is reader-agnostic (0-100) so a progress bar can be drawn without
-- understanding location's format.
CREATE TABLE IF NOT EXISTS reading_progress (
	user_id    TEXT NOT NULL,
	file_id    TEXT NOT NULL REFERENCES indexed_files(id) ON DELETE CASCADE,
	location   TEXT NOT NULL DEFAULT '',
	percent    REAL NOT NULL DEFAULT 0,
	updated_at TEXT NOT NULL,
	PRIMARY KEY (user_id, file_id)
);

-- collections are personal, user-curated groupings of files (a book can be
-- in many collections at once) — unlike tags, which are a shared library
-- vocabulary, a collection belongs to the user who made it. pinned surfaces
-- it first on the home dashboard (later phase).
CREATE TABLE IF NOT EXISTS collections (
	id          TEXT PRIMARY KEY,
	user_id     TEXT NOT NULL,
	name        TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	pinned      INTEGER NOT NULL DEFAULT 0,
	created_at  TEXT NOT NULL,
	updated_at  TEXT NOT NULL,
	UNIQUE (user_id, name COLLATE NOCASE)
);

CREATE INDEX IF NOT EXISTS collections_user_idx ON collections (user_id);

-- collection_items is the many-to-many join between collections and files.
CREATE TABLE IF NOT EXISTS collection_items (
	collection_id TEXT NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
	file_id       TEXT NOT NULL REFERENCES indexed_files(id) ON DELETE CASCADE,
	added_at      TEXT NOT NULL,
	PRIMARY KEY (collection_id, file_id)
);

CREATE INDEX IF NOT EXISTS collection_items_file_idx ON collection_items (file_id);

-- bookmarks mark a specific place in a book a user wants to return to. A
-- user can have many bookmarks per file (unlike reading_progress, which is
-- a single resume point), so each row gets its own id rather than a
-- (user_id, file_id) primary key. location is opaque to the server, exactly
-- like reading_progress.location (page number / EPUB CFI / scroll anchor),
-- meaningful only to the reader that wrote it.
CREATE TABLE IF NOT EXISTS bookmarks (
	id         TEXT PRIMARY KEY,
	user_id    TEXT NOT NULL,
	file_id    TEXT NOT NULL REFERENCES indexed_files(id) ON DELETE CASCADE,
	location   TEXT NOT NULL,
	label      TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS bookmarks_user_file_idx ON bookmarks (user_id, file_id);
CREATE INDEX IF NOT EXISTS bookmarks_file_idx ON bookmarks (file_id);

-- highlights capture a passage of text a user selected in a reader, along
-- with an optional note and a highlight color. location is the same opaque
-- reader-position string used by reading_progress/bookmarks; text is a copy
-- of the highlighted passage (kept even if the underlying file changes).
CREATE TABLE IF NOT EXISTS highlights (
	id         TEXT PRIMARY KEY,
	user_id    TEXT NOT NULL,
	file_id    TEXT NOT NULL REFERENCES indexed_files(id) ON DELETE CASCADE,
	location   TEXT NOT NULL,
	text       TEXT NOT NULL DEFAULT '',
	color      TEXT NOT NULL DEFAULT 'yellow',
	note       TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS highlights_user_file_idx ON highlights (user_id, file_id);
CREATE INDEX IF NOT EXISTS highlights_file_idx ON highlights (file_id);

-- saved_words are words a user looked up in the reader's dictionary and
-- kept, with the meaning they read (copied, so it survives the dictionary
-- being removed). location is the same opaque reader position as
-- highlights; the reader paints the word there in gray.
CREATE TABLE IF NOT EXISTS saved_words (
	id         TEXT PRIMARY KEY,
	user_id    TEXT NOT NULL,
	file_id    TEXT NOT NULL REFERENCES indexed_files(id) ON DELETE CASCADE,
	word       TEXT NOT NULL,
	location   TEXT NOT NULL,
	text       TEXT NOT NULL DEFAULT '',
	definition TEXT NOT NULL DEFAULT '',
	source     TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS saved_words_user_file_idx ON saved_words (user_id, file_id);
CREATE INDEX IF NOT EXISTS saved_words_file_idx ON saved_words (file_id);

-- quotes are passages a user has saved out of a book together with their
-- own commentary, independent of any highlight (a quote can be typed in by
-- hand rather than selected in a reader). location is optional and, when
-- present, lets "jump to quote" reuse the same reader navigation hook as
-- bookmarks/highlights. author/chapter/page are free-text citation fields
-- captured at save time so they survive even if book_metadata is edited
-- later.
CREATE TABLE IF NOT EXISTS quotes (
	id         TEXT PRIMARY KEY,
	user_id    TEXT NOT NULL,
	file_id    TEXT NOT NULL REFERENCES indexed_files(id) ON DELETE CASCADE,
	text       TEXT NOT NULL,
	location   TEXT NOT NULL DEFAULT '',
	author     TEXT NOT NULL DEFAULT '',
	chapter    TEXT NOT NULL DEFAULT '',
	page       TEXT NOT NULL DEFAULT '',
	commentary TEXT NOT NULL DEFAULT '',
	favorite   INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS quotes_user_file_idx ON quotes (user_id, file_id);
CREATE INDEX IF NOT EXISTS quotes_file_idx ON quotes (file_id);
CREATE INDEX IF NOT EXISTS quotes_user_favorite_idx ON quotes (user_id, favorite);

-- fts_content is a second FTS5 table (separate from fts_files, which stays
-- filename-only) indexing the content of annotations and extracted book
-- text, so search can find a term inside a book or inside what a user
-- wrote about it, not just in a filename. source_type/source_id identify
-- the underlying row ('metadata'/'book_text' use file_id as source_id,
-- since neither has an id of its own); file_id scopes a hit to a book;
-- user_id scopes per-user content (quote/highlight) so a search never
-- surfaces another user's writing —
-- empty for library-wide content (book_text/metadata), visible to anyone
-- with library access. Quotes/highlights are kept in sync by
-- triggers below (safe: they are never written via UPSERT, so there is no
-- INSERT-vs-UPDATE trigger ambiguity to worry about). book_metadata/
-- book_text are synced explicitly by internal/bookmeta instead — bookmeta
-- a) writes via UPSERT, where trigger-firing semantics are a needless risk
-- to depend on, and b) book_text has no DB row to trigger off of at all
-- (it lives in a file under .orchard/text/). A DELETE trigger on
-- book_metadata still handles cleanup on cascade-delete, since that's a
-- plain DELETE regardless of how it was triggered.
CREATE VIRTUAL TABLE IF NOT EXISTS fts_content USING fts5(
	source_type UNINDEXED,
	source_id UNINDEXED,
	file_id UNINDEXED,
	user_id UNINDEXED,
	text
);

CREATE TRIGGER IF NOT EXISTS fts_content_book_metadata_delete AFTER DELETE ON book_metadata BEGIN
	DELETE FROM fts_content WHERE source_type IN ('metadata', 'book_text') AND source_id = old.file_id;
END;

CREATE TRIGGER IF NOT EXISTS fts_content_quotes_insert AFTER INSERT ON quotes BEGIN
	INSERT INTO fts_content (source_type, source_id, file_id, user_id, text)
	VALUES ('quote', new.id, new.file_id, new.user_id, trim(new.text || ' ' || new.commentary));
END;
CREATE TRIGGER IF NOT EXISTS fts_content_quotes_update AFTER UPDATE ON quotes BEGIN
	DELETE FROM fts_content WHERE source_type = 'quote' AND source_id = old.id;
	INSERT INTO fts_content (source_type, source_id, file_id, user_id, text)
	VALUES ('quote', new.id, new.file_id, new.user_id, trim(new.text || ' ' || new.commentary));
END;
CREATE TRIGGER IF NOT EXISTS fts_content_quotes_delete AFTER DELETE ON quotes BEGIN
	DELETE FROM fts_content WHERE source_type = 'quote' AND source_id = old.id;
END;

CREATE TRIGGER IF NOT EXISTS fts_content_highlights_insert AFTER INSERT ON highlights BEGIN
	INSERT INTO fts_content (source_type, source_id, file_id, user_id, text)
	VALUES ('highlight', new.id, new.file_id, new.user_id, trim(new.text || ' ' || new.note));
END;
CREATE TRIGGER IF NOT EXISTS fts_content_highlights_update AFTER UPDATE ON highlights BEGIN
	DELETE FROM fts_content WHERE source_type = 'highlight' AND source_id = old.id;
	INSERT INTO fts_content (source_type, source_id, file_id, user_id, text)
	VALUES ('highlight', new.id, new.file_id, new.user_id, trim(new.text || ' ' || new.note));
END;
CREATE TRIGGER IF NOT EXISTS fts_content_highlights_delete AFTER DELETE ON highlights BEGIN
	DELETE FROM fts_content WHERE source_type = 'highlight' AND source_id = old.id;
END;

-- fts_content_state is a tiny marker table so the one-time backfill in
-- addFTSContent is cheap to skip on every subsequent Open() (migrate runs
-- per-Open, and Open is called per request by several handlers).
CREATE TABLE IF NOT EXISTS fts_content_state (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);

-- recent_searches is a per-user log of search queries, upserted by text so
-- repeating a search just bumps its timestamp rather than duplicating rows.
-- Trimmed to a bounded count per user at write time (see internal/
-- recentsearch) rather than left to grow unbounded.
CREATE TABLE IF NOT EXISTS recent_searches (
	user_id     TEXT NOT NULL,
	query       TEXT NOT NULL,
	searched_at TEXT NOT NULL,
	PRIMARY KEY (user_id, query)
);

CREATE INDEX IF NOT EXISTS recent_searches_user_time_idx ON recent_searches (user_id, searched_at);

-- library_members records who each user_id in this library belongs to, by
-- username. user_ids are random per Orchard install, so without this a
-- library opened on a fresh install would hold everyone's reading data
-- under ids nobody has any more (see members.go).
CREATE TABLE IF NOT EXISTS library_members (
	user_id    TEXT PRIMARY KEY,
	username   TEXT NOT NULL COLLATE NOCASE,
	updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS library_members_username_idx ON library_members (username COLLATE NOCASE);

-- member_settings holds a member's preferences that travel with the
-- library (reader preferences, Home layout), as JSON values by key.
CREATE TABLE IF NOT EXISTS member_settings (
	user_id    TEXT NOT NULL,
	key        TEXT NOT NULL,
	value      TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	PRIMARY KEY (user_id, key)
);
`
