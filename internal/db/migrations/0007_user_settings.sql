-- 0007_user_settings: per-user preferences stored on the server.
--
-- Key/value rows scoped to one account, so a preference follows the user
-- across browsers and future native clients. The first consumer is the
-- Memories section of Settings (internal/usersettings): slideshow interval,
-- edited-copy behaviour, default image layout, default editor mode and
-- autosave. Values are JSON-encoded scalars.

CREATE TABLE IF NOT EXISTS user_settings (
	user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	key        TEXT NOT NULL,
	value      TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	PRIMARY KEY (user_id, key)
);
