package db

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"
)

// AppliedMigration records one successfully applied migration.
type AppliedMigration struct {
	Version   int
	Name      string
	AppliedAt string
}

// migrationsFS is the embedded set of SQL migration files. They are applied
// in ascending numeric order; each file is applied exactly once, inside its
// own transaction.
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrate applies all pending migrations to the database at db. It is
// idempotent: running it again on an up-to-date database is a no-op.
//
// Migration files live in migrations/ and are named NNNN_description.sql,
// where NNNN is a zero-padded monotonically increasing version number. Never
// edit an applied migration; add a new file instead.
func Migrate(db *sql.DB) error {
	return MigrateFS(db, migrationsFS, "migrations")
}

// MigrateFS applies pending migrations from an arbitrary fs.FS. It exists so
// tests can exercise the runner with synthetic migration sets.
func MigrateFS(db *sql.DB, fsys fs.FS, dir string) error {
	if err := ensureMigrationsTable(db); err != nil {
		return err
	}

	applied, err := appliedVersions(db)
	if err != nil {
		return err
	}

	entries, err := loadMigrationEntries(fsys, dir)
	if err != nil {
		return err
	}

	for _, e := range entries {
		if _, ok := applied[e.Version]; ok {
			continue
		}
		if err := applyMigration(db, e); err != nil {
			return err
		}
	}
	return nil
}

// LatestVersion returns the highest applied migration version. It returns 0
// when no migrations have been applied.
func LatestVersion(db *sql.DB) (int, error) {
	var v sql.NullInt64
	err := db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&v)
	if err != nil {
		return 0, fmt.Errorf("query latest migration version: %w", err)
	}
	if !v.Valid {
		return 0, nil
	}
	return int(v.Int64), nil
}

type migrationEntry struct {
	Version  int
	FileName string
	SQL      string
}

func ensureMigrationsTable(db *sql.DB) error {
	const query = `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    INTEGER PRIMARY KEY,
			name       TEXT NOT NULL,
			applied_at TEXT NOT NULL
		)`
	if _, err := db.Exec(query); err != nil {
		return fmt.Errorf("create schema_migrations table: %w", err)
	}
	return nil
}

func appliedVersions(db *sql.DB) (map[int]struct{}, error) {
	rows, err := db.Query(`SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("read applied migrations: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make(map[int]struct{})
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("scan applied migration: %w", err)
		}
		out[v] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate applied migrations: %w", err)
	}
	return out, nil
}

func loadMigrationEntries(fsys fs.FS, dir string) ([]migrationEntry, error) {
	dirEntries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("read migrations directory: %w", err)
	}

	var entries []migrationEntry
	for _, de := range dirEntries {
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".sql") {
			continue
		}
		version, _, err := parseMigrationName(de.Name())
		if err != nil {
			return nil, err
		}
		body, err := fs.ReadFile(fsys, path.Join(dir, de.Name()))
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", de.Name(), err)
		}
		entries = append(entries, migrationEntry{
			Version:  version,
			FileName: de.Name(),
			SQL:      string(body),
		})
	}

	if len(entries) == 0 {
		return nil, errors.New("no migration files found")
	}

	// Insertion-sort by version; migration sets are small and this keeps the
	// dependency footprint at zero.
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0 && entries[j].Version < entries[j-1].Version; j-- {
			entries[j], entries[j-1] = entries[j-1], entries[j]
		}
	}

	for i := 1; i < len(entries); i++ {
		if entries[i].Version == entries[i-1].Version {
			return nil, fmt.Errorf("duplicate migration version %d", entries[i].Version)
		}
	}
	return entries, nil
}

// parseMigrationName parses "0001_initial_schema.sql" into (1, "initial_schema").
func parseMigrationName(filename string) (int, string, error) {
	base := strings.TrimSuffix(filename, ".sql")
	sep := strings.IndexByte(base, '_')
	if sep <= 0 {
		return 0, "", fmt.Errorf("invalid migration filename %q: want NNNN_description.sql", filename)
	}
	var version int
	if _, err := fmt.Sscanf(base[:sep], "%d", &version); err != nil {
		return 0, "", fmt.Errorf("invalid migration version in %q: %w", filename, err)
	}
	name := base[sep+1:]
	if name == "" {
		return 0, "", fmt.Errorf("invalid migration filename %q: missing description", filename)
	}
	return version, name, nil
}

// applyMigration runs one migration file inside a transaction. The migration
// and its schema_migrations row commit together or roll back together.
func applyMigration(db *sql.DB, e migrationEntry) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin migration %d: %w", e.Version, err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(e.SQL); err != nil {
		return fmt.Errorf("apply migration %s: %w", e.FileName, err)
	}

	const record = `INSERT INTO schema_migrations (version, name, applied_at)
		VALUES (?, ?, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))`
	if _, err := tx.Exec(record, e.Version, e.FileName); err != nil {
		return fmt.Errorf("record migration %d: %w", e.Version, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %d: %w", e.Version, err)
	}
	return nil
}
