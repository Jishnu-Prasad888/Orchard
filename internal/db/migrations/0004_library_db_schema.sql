-- 0004_library_db_schema: per-library file index and background job queue.
--
-- This migration runs against the SERVER-LEVEL database to record the schema
-- version used when a library-level database was last initialised. The actual
-- per-library tables (indexed_files, index_jobs) live in each library's own
-- SQLite database at <root>/.cairn/library.db and are created by the library
-- database initialisation code.
--
-- Having this migration in the server schema lets us track which libraries
-- need their per-library DB refreshed when the library schema version bumps.
-- For now the column is informational; the indexer reads it and compares
-- against the current LibraryDBSchemaVersion constant.

ALTER TABLE libraries ADD COLUMN lib_db_schema_version INTEGER NOT NULL DEFAULT 0;
