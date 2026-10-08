-- 0001_initial: server-level schema baseline.
--
-- Server-level tables hold instance-scoped data: users, sessions, roles,
-- settings, registered libraries, global permissions, share links, and audit
-- records. Library-level data (files, albums, tags, memories, ...) lives in
-- per-library databases inside each library's metadata directory and is
-- migrated separately.

-- Global key/value settings for the server instance itself.
CREATE TABLE server_settings (
	key        TEXT PRIMARY KEY,
	value      TEXT NOT NULL,
	updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);