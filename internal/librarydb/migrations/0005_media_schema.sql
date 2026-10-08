-- 0005_media_schema: extend the per-library database with media metadata and
-- folder tracking.
--
-- This SQL is applied to each library's <root>/.cairn/library.db by the
-- librarydb migration runner (CREATE TABLE IF NOT EXISTS is idempotent).

-- folders tracks every directory under the library root that contains at
-- least one indexed file. Relative paths are always forward-slash separated.
CREATE TABLE IF NOT EXISTS folders (
	id           TEXT PRIMARY KEY,
	rel_path     TEXT NOT NULL UNIQUE,
	parent_id    TEXT REFERENCES folders(id),
	name         TEXT NOT NULL,
	file_count   INTEGER NOT NULL DEFAULT 0,
	created_at   TEXT NOT NULL,
	updated_at   TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS folders_rel_path_idx  ON folders (rel_path);
CREATE INDEX IF NOT EXISTS folders_parent_idx    ON folders (parent_id);

-- media_metadata stores media-specific attributes extracted from indexed files.
-- These are derived from the original file and can be regenerated at any time.
-- The row is keyed on the indexed_files.id so it can be joined efficiently.
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

-- trash records soft-deleted files so they can be restored or permanently
-- deleted later. The original indexed_files row keeps its status='deleted';
-- this table stores the previous rel_path so restore can put it back.
CREATE TABLE IF NOT EXISTS trash (
	file_id         TEXT PRIMARY KEY REFERENCES indexed_files(id) ON DELETE CASCADE,
	original_path   TEXT NOT NULL,
	trash_path      TEXT NOT NULL,
	deleted_at      TEXT NOT NULL,
	deleted_by      TEXT
);
