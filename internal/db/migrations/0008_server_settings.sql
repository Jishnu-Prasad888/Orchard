-- 0008_server_settings: server-wide switches that an administrator changes at
-- runtime. The first is the local ML master switch ("ml.enabled"): when a row
-- exists it wins over the CAIRN_ML_ENABLED environment default. Values are
-- JSON-encoded scalars.

CREATE TABLE IF NOT EXISTS server_settings (
	key        TEXT PRIMARY KEY,
	value      TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
