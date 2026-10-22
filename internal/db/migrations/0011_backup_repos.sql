-- 0011_backup_repos: backups kept on a network server.
--
-- A backup to SFTP, FTP or SMB is a snapshot in a repository on that server
-- (internal/backups/repo.go). snapshot names it there; target is the
-- server's settings (never a password) so verify and restore know where it
-- is. Both are empty for a backup in a local folder.

ALTER TABLE backups ADD COLUMN snapshot TEXT NOT NULL DEFAULT '';
ALTER TABLE backups ADD COLUMN target TEXT NOT NULL DEFAULT '';
