-- 0010_backup_triggers: why each backup ran.
--
-- "manual" (Back up now), "scheduled" (the clock), "catch-up" (a scheduled
-- run owed while the server was off), or "size" (enough new data since the
-- last backup). Rows from before this migration were all started by hand or
-- by the old interval timer, so they read as "manual".

ALTER TABLE backups ADD COLUMN trigger TEXT NOT NULL DEFAULT 'manual';
