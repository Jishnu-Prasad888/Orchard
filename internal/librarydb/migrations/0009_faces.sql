-- 0009_faces: face observations and people for local ML (Phase 15).
--
-- Applied to each library's <root>/.cairn/library.db. These statements mirror
-- the canonical schema embedded in internal/librarydb/librarydb.go and are
-- idempotent (CREATE TABLE/INDEX IF NOT EXISTS) so they can be applied on top
-- of an existing database.

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

CREATE TABLE IF NOT EXISTS people (
	id            TEXT PRIMARY KEY,
	name          TEXT NOT NULL,
	cover_face_id TEXT REFERENCES faces(id) ON DELETE SET NULL,
	cover_file_id TEXT REFERENCES indexed_files(id) ON DELETE SET NULL,
	created_at    TEXT NOT NULL,
	updated_at    TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS people_name_idx ON people (name COLLATE NOCASE);

CREATE TABLE IF NOT EXISTS person_faces (
	person_id   TEXT NOT NULL REFERENCES people(id) ON DELETE CASCADE,
	face_id     TEXT NOT NULL REFERENCES faces(id) ON DELETE CASCADE,
	assigned_by TEXT NOT NULL CHECK (assigned_by IN ('auto', 'manual')),
	created_at  TEXT NOT NULL,
	PRIMARY KEY (person_id, face_id)
);

CREATE INDEX IF NOT EXISTS person_faces_face_idx ON person_faces (face_id);