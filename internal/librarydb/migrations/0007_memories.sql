-- 0007_memories: long-form Markdown memories with edit history and FTS search.
--
-- Applied to each library's <root>/.cairn/library.db. These statements mirror
-- the canonical schema embedded in internal/librarydb/librarydb.go and are
-- idempotent (CREATE TABLE/TRIGGER/VIRTUAL TABLE IF NOT EXISTS) so they can be
-- applied on top of an existing database.

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

CREATE TABLE IF NOT EXISTS memory_refs (
	memory_id   TEXT NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
	target_type TEXT NOT NULL
		CHECK (target_type IN ('media','memory','album','person','tag')),
	target_id   TEXT NOT NULL,
	PRIMARY KEY (memory_id, target_type, target_id)
);

CREATE INDEX IF NOT EXISTS memory_refs_target_idx ON memory_refs (target_type, target_id);

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