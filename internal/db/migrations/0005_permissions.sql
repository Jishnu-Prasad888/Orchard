-- 0005_permissions: resource-based authorization grants and public shares.
--
-- Server-level authorization per ADR-0005. Grants are stored flat with
-- path-like resource keys so permission evaluation never walks the filesystem
-- tree: a grant on a library or folder is a string prefix of every descendant.
--
-- Resource key grammar (one level per "/"):
--   L                    the library
--   L/f:dirname          a folder
--   L/f:dir/f:sub        a nested folder
--   L/f:dir/x:file.jpg   a file inside a folder
--   L/x:file.jpg         a file at the library root
--   L/a:albumID          an album
--   L/m:memoryID         a memory
--   L/t:tagID            a tag
--
-- Evaluation is most-specific-wins: for a target resource key, the grants
-- whose key is a prefix of it are collected (the grant itself at the most
-- specific, ancestors toward the library); the longest matching grant decides
-- each capability, and a deny at the same key beats an allow. Nothing matches
-- nothing: unlisted capabilities default to denied.

-- Grants: an explicit allow or deny entry for one user on one resource
-- subtree. capabilities is a comma-separated list from the fixed set
-- (read,download,create,edit,move,delete,share,manage).
CREATE TABLE permission_grants (
	id           TEXT PRIMARY KEY,
	user_id      TEXT NOT NULL,
	resource_key TEXT NOT NULL,
	capabilities TEXT NOT NULL,
	effect       TEXT NOT NULL DEFAULT 'allow' CHECK (effect IN ('allow', 'deny')),
	created_by   TEXT NOT NULL,
	created_at   TEXT NOT NULL,
	UNIQUE (user_id, resource_key, effect)
);

CREATE INDEX permission_grants_user_idx       ON permission_grants (user_id);
CREATE INDEX permission_grants_key_idx        ON permission_grants (resource_key);
CREATE INDEX permission_grants_effect_idx     ON permission_grants (effect);

-- Public shares. Only the SHA-256 of the genuinely random share token is
-- persisted, mirroring how session tokens are stored. password_hash is an
-- optional argon2id hash; a share with a password is only usable after the
-- holder presents it. expires_at and revoked_at surface the two revocation
-- mechanisms (scheduled expiry and immediate revocation).
CREATE TABLE shares (
	id            TEXT PRIMARY KEY,
	resource_key  TEXT NOT NULL,
	capabilities  TEXT NOT NULL,
	token_hash    TEXT NOT NULL UNIQUE,
	password_hash TEXT,
	expires_at    TEXT,
	revoked_at    TEXT,
	created_by    TEXT NOT NULL,
	created_at    TEXT NOT NULL
);

CREATE INDEX shares_token_idx       ON shares (token_hash);
CREATE INDEX shares_resource_idx    ON shares (resource_key);
CREATE INDEX shares_revoked_idx     ON shares (revoked_at) WHERE revoked_at IS NULL;