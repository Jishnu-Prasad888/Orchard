// Package librarydb manages per-library SQLite databases.
//
// Each Cairn library keeps its own SQLite database at <root>/.cairn/library.db.
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

	"github.com/Jishnu-Prasad888/Cairn/internal/media"

	_ "modernc.org/sqlite" // pure-Go SQLite driver
)

const (
	// dbFileName is the per-library database filename inside .cairn/.
	dbFileName = "library.db"

	// SchemaVersion is the current version of the library-level database
	// schema. Bump this when adding new tables or changing existing ones.
	SchemaVersion = 8
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
// <cairnDir>/library.db and applies any pending schema migrations.
func OpenDB(cairnDir string) (*DB, error) {
	pool, err := Open(cairnDir)
	if err != nil {
		return nil, err
	}
	return &DB{pool: pool}, nil
}

// Open opens (creating if necessary) the per-library SQLite database at
// <cairnDir>/library.db and applies any pending schema migrations.
func Open(cairnDir string) (*sql.DB, error) {
	path := filepath.Join(cairnDir, dbFileName)
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
	return upgradeMemories(db)
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

-- media_metadata stores media-specific attributes derived from indexed files.
-- Rows are regenerable; keyed on indexed_files.id for efficient joins.
CREATE TABLE IF NOT EXISTS media_metadata (
	file_id       TEXT PRIMARY KEY REFERENCES indexed_files(id) ON DELETE CASCADE,
	media_type    TEXT NOT NULL DEFAULT 'other'
		CHECK (media_type IN ('photo','video','audio','document','other')),
	mime_type     TEXT,
	width         INTEGER,
	height        INTEGER,
	duration_secs REAL,
	taken_at      TEXT,
	camera_make   TEXT,
	camera_model  TEXT,
	latitude      REAL,
	longitude     REAL,
	has_thumbnail INTEGER NOT NULL DEFAULT 0,
	updated_at    TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS media_metadata_type_idx ON media_metadata (media_type);

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

-- albums groups files into named collections without moving them on disk.
CREATE TABLE IF NOT EXISTS albums (
	id            TEXT PRIMARY KEY,
	name          TEXT NOT NULL,
	description   TEXT,
	cover_file_id TEXT REFERENCES indexed_files(id) ON DELETE SET NULL,
	created_at    TEXT NOT NULL,
	updated_at    TEXT NOT NULL
);

-- album_files is the ordered many-to-many join between albums and files.
CREATE TABLE IF NOT EXISTS album_files (
	album_id   TEXT NOT NULL REFERENCES albums(id) ON DELETE CASCADE,
	file_id    TEXT NOT NULL REFERENCES indexed_files(id) ON DELETE CASCADE,
	position   INTEGER NOT NULL DEFAULT 0,
	added_at   TEXT NOT NULL,
	PRIMARY KEY (album_id, file_id)
);

CREATE INDEX IF NOT EXISTS album_files_album_idx ON album_files (album_id, position);

-- favorites records files that the user has starred.
CREATE TABLE IF NOT EXISTS favorites (
	file_id    TEXT PRIMARY KEY REFERENCES indexed_files(id) ON DELETE CASCADE,
	created_at TEXT NOT NULL
);

-- file_notes stores per-file Markdown notes (captions, descriptions, journal
-- entries) written in the web UI. Like memories, they are app-level data and
-- live in the library database so they travel with a portable library. Each
-- file has at most one note; an empty/missing body means no note.
CREATE TABLE IF NOT EXISTS file_notes (
	file_id    TEXT PRIMARY KEY REFERENCES indexed_files(id) ON DELETE CASCADE,
	body       TEXT NOT NULL DEFAULT '',
	updated_at TEXT NOT NULL
);

-- memories are long-form Markdown documents. They are app-level data and live
-- in the library database so they travel with a portable library. deleted is a
-- soft-delete flag: deleting a memory is reversible and keeps its history.
CREATE TABLE IF NOT EXISTS memories (
	id          TEXT PRIMARY KEY,
	title       TEXT NOT NULL,
	body        TEXT NOT NULL DEFAULT '',
	memory_date TEXT,
	deleted     INTEGER NOT NULL DEFAULT 0,
	created_at  TEXT NOT NULL,
	updated_at  TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS memories_updated_idx ON memories (updated_at)
	WHERE deleted = 0;

-- memory_versions keeps the full edit history of a memory. Every autosave and
-- explicit save writes a new append-only row so any earlier state can be
-- recovered.
CREATE TABLE IF NOT EXISTS memory_versions (
	id        TEXT PRIMARY KEY,
	memory_id TEXT NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
	version   INTEGER NOT NULL,
	title     TEXT NOT NULL,
	body      TEXT NOT NULL,
	saved_at  TEXT NOT NULL,
	UNIQUE (memory_id, version)
);

CREATE INDEX IF NOT EXISTS memory_versions_memory_idx ON memory_versions (memory_id, version DESC);

-- memory_refs records every [[type:id]] reference extracted from a memory's
-- Markdown body. It is rewritten on each save so it always mirrors the body.
CREATE TABLE IF NOT EXISTS memory_refs (
	memory_id   TEXT NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
	target_type TEXT NOT NULL
		CHECK (target_type IN ('media','memory','album','person','tag')),
	target_id   TEXT NOT NULL,
	PRIMARY KEY (memory_id, target_type, target_id)
);

CREATE INDEX IF NOT EXISTS memory_refs_target_idx ON memory_refs (target_type, target_id);

-- fts_memories is an FTS5 virtual table for search over memory titles and
-- bodies. It is a normal (non-contentless) FTS5 table, matching fts_files, so
-- the UNINDEXED memory_id column is stored and can be joined back to the
-- memories table. It is kept in sync by the triggers below.
CREATE VIRTUAL TABLE IF NOT EXISTS fts_memories USING fts5(
	memory_id UNINDEXED,
	title,
	body,
	prefix='2 3'
);

CREATE TRIGGER IF NOT EXISTS fts_memories_insert AFTER INSERT ON memories BEGIN
	INSERT INTO fts_memories(memory_id, title, body) VALUES (new.id, new.title, new.body);
END;

CREATE TRIGGER IF NOT EXISTS fts_memories_update AFTER UPDATE OF title, body, deleted ON memories BEGIN
	DELETE FROM fts_memories WHERE memory_id = old.id;
	INSERT INTO fts_memories(memory_id, title, body) VALUES (new.id, new.title, new.body);
END;

CREATE TRIGGER IF NOT EXISTS fts_memories_delete AFTER DELETE ON memories BEGIN
	DELETE FROM fts_memories WHERE memory_id = old.id;
END;

-- memory_blocks is the ordered list of blocks that make up a memory
-- (notebook model, schema v8). A text block carries Markdown; an image block
-- carries a layout and slideshow settings and owns memory_images rows.
-- slideshow_interval NULL means "inherit the viewer's setting".
CREATE TABLE IF NOT EXISTS memory_blocks (
	id                 TEXT PRIMARY KEY,
	memory_id          TEXT NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
	position           INTEGER NOT NULL,
	type               TEXT NOT NULL CHECK (type IN ('text', 'image')),
	markdown           TEXT NOT NULL DEFAULT '',
	layout             TEXT NOT NULL DEFAULT '',
	slideshow          INTEGER NOT NULL DEFAULT 0,
	slideshow_interval INTEGER,
	created_at         TEXT NOT NULL,
	updated_at         TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS memory_blocks_memory_idx ON memory_blocks (memory_id, position);

-- memory_images are references from an image block to library media. They
-- carry Memory-specific presentation (caption, crop, rotation, filter,
-- adjustments) so the same photo can appear in many memories independently.
-- source_file_id deliberately has no foreign key: a missing or re-indexed
-- original must not cascade away the reference, its caption, or its position.
-- source_rel_path/source_hash let the store re-attach a moved original.
CREATE TABLE IF NOT EXISTS memory_images (
	id              TEXT PRIMARY KEY,
	memory_id       TEXT NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
	block_id        TEXT NOT NULL REFERENCES memory_blocks(id) ON DELETE CASCADE,
	position        INTEGER NOT NULL,
	source_file_id  TEXT NOT NULL,
	source_rel_path TEXT NOT NULL DEFAULT '',
	source_hash     TEXT NOT NULL DEFAULT '',
	caption         TEXT NOT NULL DEFAULT '',
	crop_x          REAL,
	crop_y          REAL,
	crop_w          REAL,
	crop_h          REAL,
	rotation        INTEGER NOT NULL DEFAULT 0 CHECK (rotation IN (0, 90, 180, 270)),
	filter          TEXT NOT NULL DEFAULT 'original',
	brightness      INTEGER NOT NULL DEFAULT 0,
	contrast        INTEGER NOT NULL DEFAULT 0,
	saturation      INTEGER NOT NULL DEFAULT 0,
	derived_id      TEXT,
	created_at      TEXT NOT NULL,
	updated_at      TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS memory_images_block_idx  ON memory_images (block_id, position);
CREATE INDEX IF NOT EXISTS memory_images_memory_idx ON memory_images (memory_id);
CREATE INDEX IF NOT EXISTS memory_images_source_idx ON memory_images (source_file_id);

-- memory_derived_media records edited copies rendered for a memory image when
-- the "create edited copies" setting is on. Files live under
-- .cairn/memory-media/<memory-id>/ (rel_path is relative to .cairn/), which
-- the indexer never scans. Rows are regenerable and are garbage collected only
-- once no memory_images row references them.
CREATE TABLE IF NOT EXISTS memory_derived_media (
	id              TEXT PRIMARY KEY,
	memory_id       TEXT NOT NULL,
	memory_image_id TEXT NOT NULL,
	source_file_id  TEXT NOT NULL,
	rel_path        TEXT NOT NULL UNIQUE,
	edit_signature  TEXT NOT NULL,
	width           INTEGER NOT NULL,
	height          INTEGER NOT NULL,
	size_bytes      INTEGER NOT NULL,
	created_at      TEXT NOT NULL,
	updated_at      TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS memory_derived_image_idx ON memory_derived_media (memory_image_id);

-- memory_tags attaches library tags to memories (metadata panel).
CREATE TABLE IF NOT EXISTS memory_tags (
	memory_id  TEXT NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
	tag_id     TEXT NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
	created_at TEXT NOT NULL,
	PRIMARY KEY (memory_id, tag_id)
);

CREATE INDEX IF NOT EXISTS memory_tags_tag_idx ON memory_tags (tag_id);

-- ml_signatures stores per-file similarity signatures produced by the local
-- ML subsystem (Phase 11). Derived, removable data: purging the table never
-- touches originals and signatures can be regenerated. provider + version
-- identify the algorithm so a future provider change cannot corrupt rows.
CREATE TABLE IF NOT EXISTS ml_signatures (
	file_id    TEXT PRIMARY KEY REFERENCES indexed_files(id) ON DELETE CASCADE,
	provider   TEXT NOT NULL,
	version    INTEGER NOT NULL,
	signature  INTEGER NOT NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS ml_signatures_provider_idx ON ml_signatures (provider);

-- faces stores per-image face observations produced by the local ML face
-- capability (Phase 15). Derived, regenerable data with the same lifecycle as
-- ml_signatures: purging never touches originals and a pass can rebuild it.
-- provider + version identify the detection/embedding algorithm so a future
-- provider change cannot confuse clusters. descriptor is the per-face
-- appearance vector used for clustering (normalized float32 BLOB).
CREATE TABLE IF NOT EXISTS faces (
	id          TEXT PRIMARY KEY,
	file_id     TEXT NOT NULL REFERENCES indexed_files(id) ON DELETE CASCADE,
	provider    TEXT NOT NULL,
	version     INTEGER NOT NULL,
	x           INTEGER NOT NULL,
	y           INTEGER NOT NULL,
	width       INTEGER NOT NULL,
	height      INTEGER NOT NULL,
	confidence  REAL NOT NULL,
	descriptor  BLOB NOT NULL,
	created_at  TEXT NOT NULL,
	updated_at  TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS faces_file_idx     ON faces (file_id);
CREATE INDEX IF NOT EXISTS faces_provider_idx ON faces (provider);

-- people are user-curated groupings of faces. Names live here and survive a
-- face purge; the cover references become NULL when their faces are removed.
CREATE TABLE IF NOT EXISTS people (
	id            TEXT PRIMARY KEY,
	name          TEXT NOT NULL,
	cover_face_id TEXT REFERENCES faces(id) ON DELETE SET NULL,
	cover_file_id TEXT REFERENCES indexed_files(id) ON DELETE SET NULL,
	created_at    TEXT NOT NULL,
	updated_at    TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS people_name_idx ON people (name COLLATE NOCASE);

-- person_faces assigns faces to people. assigned_by distinguishes automatic
-- clustering from manual assignment so cluster passes never clobber a user's
-- explicit decisions.
CREATE TABLE IF NOT EXISTS person_faces (
	person_id   TEXT NOT NULL REFERENCES people(id) ON DELETE CASCADE,
	face_id     TEXT NOT NULL REFERENCES faces(id) ON DELETE CASCADE,
	assigned_by TEXT NOT NULL CHECK (assigned_by IN ('auto', 'manual')),
	created_at  TEXT NOT NULL,
	PRIMARY KEY (person_id, face_id)
);

CREATE INDEX IF NOT EXISTS person_faces_face_idx ON person_faces (face_id);

-- face_scans records which photos face detection has already analysed (and
-- with which algorithm), including photos where it found no face, so they
-- are not analysed again on every pass.
CREATE TABLE IF NOT EXISTS face_scans (
	file_id    TEXT PRIMARY KEY REFERENCES indexed_files(id) ON DELETE CASCADE,
	provider   TEXT NOT NULL,
	version    INTEGER NOT NULL,
	faces      INTEGER NOT NULL,
	scanned_at TEXT NOT NULL
);

-- face_holds lists faces a person took off someone ("Not …"): automatic
-- grouping leaves them alone until a person places them by hand.
CREATE TABLE IF NOT EXISTS face_holds (
	face_id    TEXT PRIMARY KEY REFERENCES faces(id) ON DELETE CASCADE,
	created_at TEXT NOT NULL
);
`
