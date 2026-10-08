-- 0012_face_scans: which photos face detection has already analysed, and
-- which faces a person held back from automatic grouping.
--
-- Applied to each library's <root>/.cairn/library.db. This file documents the
-- DDL that internal/librarydb/librarydb.go applies; the Go code is canonical.
-- Photos with no faces are recorded too, so they are not analysed again on
-- every pass; a change of provider or version re-queues every photo.

CREATE TABLE IF NOT EXISTS face_scans (
	file_id    TEXT PRIMARY KEY REFERENCES indexed_files(id) ON DELETE CASCADE,
	provider   TEXT NOT NULL,
	version    INTEGER NOT NULL,
	faces      INTEGER NOT NULL,
	scanned_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS face_holds (
	face_id    TEXT PRIMARY KEY REFERENCES faces(id) ON DELETE CASCADE,
	created_at TEXT NOT NULL
);
