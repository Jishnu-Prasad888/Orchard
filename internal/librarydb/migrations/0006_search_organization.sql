-- 0006_search_organization: full-text search, tags, albums, and favorites.
--
-- Applied to each library's <root>/.cairn/library.db. All statements use
-- CREATE TABLE/TRIGGER IF NOT EXISTS so this migration is idempotent.

-- fts_files is a FTS5 virtual table that enables fast full-text search over
-- file paths and names. It is kept in sync with indexed_files by triggers.
-- content='' makes it a contentless FTS5 table; the application JOINs back
-- to indexed_files for the actual columns.
CREATE VIRTUAL TABLE IF NOT EXISTS fts_files USING fts5(
	file_id UNINDEXED,
	rel_path,
	name,
	content=''
);

-- Populate fts_files from existing indexed_files rows (idempotent via DELETE first).
DELETE FROM fts_files;
INSERT INTO fts_files(file_id, rel_path, name)
SELECT id, rel_path, REPLACE(REPLACE(rel_path, '\', '/'),
	RTRIM(REPLACE(rel_path, '\', '/'), REPLACE(REPLACE(rel_path, '\', '/'), '/', '')), '')
FROM indexed_files;

-- Keep fts_files in sync as indexed_files changes.
CREATE TRIGGER IF NOT EXISTS fts_files_insert AFTER INSERT ON indexed_files BEGIN
	INSERT INTO fts_files(file_id, rel_path, name)
	VALUES (new.id, new.rel_path,
		CASE WHEN instr(new.rel_path, '/') > 0
			THEN substr(new.rel_path, length(new.rel_path) - length(new.rel_path) + instr(reverse(new.rel_path), '/') )
			ELSE new.rel_path
		END
	);
END;

CREATE TRIGGER IF NOT EXISTS fts_files_update AFTER UPDATE OF rel_path ON indexed_files BEGIN
	DELETE FROM fts_files WHERE file_id = old.id;
	INSERT INTO fts_files(file_id, rel_path, name)
	VALUES (new.id, new.rel_path,
		CASE WHEN instr(new.rel_path, '/') > 0
			THEN substr(new.rel_path, length(new.rel_path) - length(new.rel_path) + instr(reverse(new.rel_path), '/') )
			ELSE new.rel_path
		END
	);
END;

CREATE TRIGGER IF NOT EXISTS fts_files_delete AFTER DELETE ON indexed_files BEGIN
	DELETE FROM fts_files WHERE file_id = old.id;
END;

-- tags holds named labels that can be attached to any indexed file.
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

-- albums groups files into named collections without moving them.
CREATE TABLE IF NOT EXISTS albums (
	id          TEXT PRIMARY KEY,
	name        TEXT NOT NULL,
	description TEXT,
	cover_file_id TEXT REFERENCES indexed_files(id) ON DELETE SET NULL,
	created_at  TEXT NOT NULL,
	updated_at  TEXT NOT NULL
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

-- favorites records which files a user has starred.
CREATE TABLE IF NOT EXISTS favorites (
	file_id    TEXT PRIMARY KEY REFERENCES indexed_files(id) ON DELETE CASCADE,
	created_at TEXT NOT NULL
);
