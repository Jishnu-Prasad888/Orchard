-- 0010_notes: per-file Markdown notes (captions and descriptions).
--
-- Applied to each library's <root>/.cairn/library.db. All statements use
-- CREATE TABLE IF NOT EXISTS so this migration is idempotent. This file
-- mirrors the canonical DDL kept in internal/librarydb/librarydb.go.

-- file_notes stores per-file Markdown notes written in the web UI. App-level
-- data that travels with a portable library; each file has at most one note.
CREATE TABLE IF NOT EXISTS file_notes (
	file_id    TEXT PRIMARY KEY REFERENCES indexed_files(id) ON DELETE CASCADE,
	body       TEXT NOT NULL DEFAULT '',
	updated_at TEXT NOT NULL
);