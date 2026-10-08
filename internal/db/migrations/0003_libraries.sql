-- 0003_libraries: server-level registrar for storage libraries.
--
-- A library is any existing directory Cairn catalogs in place (ADR-0004). The
-- registrar tracks registered libraries at server scope; the per-library
-- identity and configuration live inside the library at <root>/.cairn/.
--
-- identity is NOT the mount path: each library carries a persistent id inside
-- its .cairn/library.json, so a disk reconnected at a different path is
-- recognized as the same library. volume_id is a best-effort filesystem
-- identity used as a secondary signal when .cairn is not found at a path.

CREATE TABLE libraries (
	id             TEXT PRIMARY KEY,
	name           TEXT NOT NULL,
	root           TEXT NOT NULL,
	status         TEXT NOT NULL DEFAULT 'offline' CHECK (status IN ('online', 'offline')),
	volume_id      TEXT,
	schema_version INTEGER NOT NULL DEFAULT 1,
	created_at     TEXT NOT NULL,
	updated_at     TEXT NOT NULL
);

CREATE INDEX libraries_root_idx ON libraries (root);
CREATE INDEX libraries_status_idx ON libraries (status);