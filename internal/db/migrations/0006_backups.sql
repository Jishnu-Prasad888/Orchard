-- 0006_backups: server-level backup records.
--
-- Each row tracks one backup run: where it was written, what it contained,
-- verification results, and the retention lifecycle state. Passphrases and
-- backup keys are never stored here; encryption salt/format lives inside the
-- backup itself (docs/backups.md, ADR-0008).

CREATE TABLE IF NOT EXISTS backups (
	id                TEXT PRIMARY KEY,
	status            TEXT NOT NULL
		CHECK (status IN ('running', 'completed', 'failed', 'pruned', 'restoring')),
	destination       TEXT NOT NULL,
	started_at        TEXT NOT NULL,
	finished_at       TEXT,
	server_db_path    TEXT NOT NULL,
	libraries         INTEGER NOT NULL DEFAULT 0,
	files             INTEGER NOT NULL DEFAULT 0,
	files_skipped     INTEGER NOT NULL DEFAULT 0,
	bytes             INTEGER NOT NULL DEFAULT 0,
	stored_bytes      INTEGER NOT NULL DEFAULT 0,
	encrypted         INTEGER NOT NULL DEFAULT 0,
	same_device       INTEGER NOT NULL DEFAULT 0,
	verify_status     TEXT,
	verify_checked    INTEGER NOT NULL DEFAULT 0,
	verify_errors     INTEGER NOT NULL DEFAULT 0,
	error_msg         TEXT,
	created_at        TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS backups_created_at_idx ON backups (created_at DESC);