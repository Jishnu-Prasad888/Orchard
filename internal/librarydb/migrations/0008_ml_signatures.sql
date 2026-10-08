-- 0008_ml_signatures: per-file similarity signatures for local ML (Phase 11).
--
-- Applied to each library's <root>/.cairn/library.db. These statements mirror
-- the canonical schema embedded in internal/librarydb/librarydb.go and are
-- idempotent (CREATE TABLE/INDEX IF NOT EXISTS) so they can be applied on top
-- of an existing database.

CREATE TABLE IF NOT EXISTS ml_signatures (
	file_id    TEXT PRIMARY KEY REFERENCES indexed_files(id) ON DELETE CASCADE,
	provider   TEXT NOT NULL,
	version    INTEGER NOT NULL,
	signature  INTEGER NOT NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS ml_signatures_provider_idx ON ml_signatures (provider);