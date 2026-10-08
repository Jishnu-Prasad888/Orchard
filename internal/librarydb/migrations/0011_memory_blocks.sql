-- 0011_memory_blocks: notebook-style memories (ordered text and image blocks).
--
-- Applied to each library's <root>/.cairn/library.db. This file documents the
-- DDL that internal/librarydb/librarydb.go applies; the Go code is canonical.
-- Tables use CREATE TABLE IF NOT EXISTS. The column additions to memories and
-- memory_versions are applied by upgradeMemories() only when the column is
-- missing (SQLite has no ADD COLUMN IF NOT EXISTS), and the FTS triggers are
-- replaced once so they index search_text instead of body.
--
-- See docs/memories.md and docs/adr/0015-memory-block-documents.md.

-- New columns on memories (added by upgradeMemories):
--   description     TEXT NOT NULL DEFAULT ''
--   location        TEXT NOT NULL DEFAULT ''
--   cover_file_id   TEXT                        -- reference, never a copy
--   revision        INTEGER NOT NULL DEFAULT 1  -- optimistic concurrency
--   search_text     TEXT NOT NULL DEFAULT ''    -- markdown + captions + tags
--   blocks_migrated INTEGER NOT NULL DEFAULT 0  -- legacy body converted
-- New column on memory_versions:
--   document        TEXT                        -- JSON snapshot of blocks

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

CREATE TABLE IF NOT EXISTS memory_tags (
	memory_id  TEXT NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
	tag_id     TEXT NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
	created_at TEXT NOT NULL,
	PRIMARY KEY (memory_id, tag_id)
);

CREATE INDEX IF NOT EXISTS memory_tags_tag_idx ON memory_tags (tag_id);

-- FTS triggers are recreated to index search_text:
CREATE TRIGGER IF NOT EXISTS fts_memories_insert AFTER INSERT ON memories BEGIN
	INSERT INTO fts_memories(memory_id, title, body) VALUES (new.id, new.title, new.search_text);
END;

CREATE TRIGGER IF NOT EXISTS fts_memories_update AFTER UPDATE OF title, body, deleted, search_text ON memories BEGIN
	DELETE FROM fts_memories WHERE memory_id = old.id;
	INSERT INTO fts_memories(memory_id, title, body) VALUES (new.id, new.title, new.search_text);
END;
