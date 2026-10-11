-- 0009_pending_signups: public "request an account" queue.
--
-- When an administrator turns on the server_settings key
-- "signup.requests_enabled", a visitor without credentials can ask for an
-- account instead of needing one created for them. The request sits here,
-- unprivileged, until an administrator approves it (creating the real user
-- from the already-hashed password) or denies it (discarding the row).

CREATE TABLE IF NOT EXISTS pending_signups (
	id            TEXT PRIMARY KEY,
	username      TEXT NOT NULL UNIQUE COLLATE NOCASE,
	password_hash TEXT NOT NULL,
	requested_at  TEXT NOT NULL,
	ip_address    TEXT,
	user_agent    TEXT
);
