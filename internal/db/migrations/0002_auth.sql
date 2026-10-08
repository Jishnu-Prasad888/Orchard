-- 0002_auth: users, sessions, and audit logging.
--
-- Server-level authentication: user accounts with argon2id password hashes,
-- opaque session tokens (stored only as hashes, never in plaintext), and an
-- audit log for security-relevant events.
--
-- Roles are recorded on the user. Handler-level resource authorization
-- supersedes ad-hoc role checks; the column remains the initial
-- Admin/User convenience layer described in docs/permissions.md.

-- User accounts.
CREATE TABLE users (
	id            TEXT PRIMARY KEY,
	username      TEXT NOT NULL UNIQUE COLLATE NOCASE,
	password_hash TEXT NOT NULL,
	role          TEXT NOT NULL DEFAULT 'user' CHECK (role IN ('admin', 'user')),
	is_enabled    INTEGER NOT NULL DEFAULT 1,
	created_at    TEXT NOT NULL,
	updated_at    TEXT NOT NULL
);

CREATE INDEX users_username_idx ON users (username COLLATE NOCASE);

-- Authenticated sessions. Only the SHA-256 of the opaque token is stored, so a
-- database leak never yields usable credentials.
CREATE TABLE sessions (
	id         TEXT PRIMARY KEY,
	user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
	token_hash TEXT NOT NULL UNIQUE,
	created_at TEXT NOT NULL,
	expires_at TEXT NOT NULL,
	revoked_at TEXT,
	user_agent TEXT,
	ip_address TEXT
);

CREATE INDEX sessions_user_idx ON sessions (user_id);
CREATE INDEX sessions_expires_idx ON sessions (expires_at);

-- Audit log for security-relevant events. Metadata holds structured,
-- non-sensitive JSON context; sensitive values must never be stored here.
CREATE TABLE audit_log (
	id            TEXT PRIMARY KEY,
	user_id       TEXT,
	action        TEXT NOT NULL,
	target_user_id TEXT,
	metadata      TEXT NOT NULL DEFAULT '{}',
	ip_address    TEXT,
	user_agent    TEXT,
	created_at    TEXT NOT NULL
);

CREATE INDEX audit_log_user_idx ON audit_log (user_id);
CREATE INDEX audit_log_created_idx ON audit_log (created_at);
CREATE INDEX audit_log_action_idx ON audit_log (action);