package db

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	pool, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	return pool
}

func TestMigrateAppliesEmbeddedMigrations(t *testing.T) {
	pool := openTestDB(t)

	if err := Migrate(pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	version, err := LatestVersion(pool)
	if err != nil {
		t.Fatalf("LatestVersion: %v", err)
	}
	if version != 8 {
		t.Errorf("LatestVersion = %d, want 8", version)
	}

	// Baseline table must exist.
	var name string
	if err := pool.QueryRow(
		`SELECT name FROM sqlite_master WHERE type='table' AND name='server_settings'`,
	).Scan(&name); err != nil {
		t.Fatalf("server_settings table missing: %v", err)
	}

	// Auth tables from migration 0002, the library registrar from 0003, and
	// the permission subsystem from 0005 must exist.
	for _, table := range []string{"users", "sessions", "audit_log", "libraries", "permission_grants", "shares"} {
		q := `SELECT name FROM sqlite_master WHERE type='table' AND name='` + table + `'`
		if err := pool.QueryRow(q).Scan(&name); err != nil {
			t.Errorf("table %s missing: %v", table, err)
		}
	}

	// schema_migrations must record the applied migrations.
	var count int
	if err := pool.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}
	if count != 8 {
		t.Errorf("schema_migrations rows = %d, want 8", count)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	pool := openTestDB(t)

	if err := Migrate(pool); err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	if err := Migrate(pool); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}

	var count int
	if err := pool.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 8 {
		t.Errorf("schema_migrations rows = %d, want 8 after re-run", count)
	}
}

func TestMigrateFSAppliesInOrder(t *testing.T) {
	pool := openTestDB(t)

	fsys := fstest.MapFS{
		"m/0002_second.sql": {Data: []byte(`CREATE TABLE second (id INTEGER PRIMARY KEY);`)},
		"m/0001_first.sql":  {Data: []byte(`CREATE TABLE first (id INTEGER PRIMARY KEY);`)},
		"m/README.md":       {Data: []byte(`not a migration`)},
	}

	if err := MigrateFS(pool, fsys, "m"); err != nil {
		t.Fatalf("MigrateFS: %v", err)
	}

	version, err := LatestVersion(pool)
	if err != nil {
		t.Fatalf("LatestVersion: %v", err)
	}
	if version != 2 {
		t.Errorf("LatestVersion = %d, want 2", version)
	}

	// Both tables must exist regardless of directory order.
	for _, table := range []string{"first", "second"} {
		var name string
		q := `SELECT name FROM sqlite_master WHERE type='table' AND name='` + table + `'`
		if err := pool.QueryRow(q).Scan(&name); err != nil {
			t.Errorf("table %s missing: %v", table, err)
		}
	}
}

func TestMigrateFSRollsBackFailedMigration(t *testing.T) {
	pool := openTestDB(t)

	fsys := fstest.MapFS{
		"m/0001_ok.sql": {
			Data: []byte(`CREATE TABLE ok_table (id INTEGER PRIMARY KEY);`),
		},
		"m/0002_broken.sql": {
			// Valid DDL followed by invalid SQL: both must roll back.
			Data: []byte("CREATE TABLE should_rollback (id INTEGER);\nTHIS IS NOT SQL;"),
		},
	}

	err := MigrateFS(pool, fsys, "m")
	if err == nil {
		t.Fatal("expected error for broken migration")
	}
	if !strings.Contains(err.Error(), "0002_broken.sql") {
		t.Errorf("error should mention migration file, got: %v", err)
	}

	version, verr := LatestVersion(pool)
	if verr != nil {
		t.Fatalf("LatestVersion: %v", verr)
	}
	if version != 1 {
		t.Errorf("LatestVersion = %d, want 1 (failed migration not recorded)", version)
	}

	// The broken migration's DDL must have rolled back.
	var name string
	q := `SELECT name FROM sqlite_master WHERE type='table' AND name='should_rollback'`
	if err := pool.QueryRow(q).Scan(&name); err == nil {
		t.Error("broken migration partially applied: should_rollback exists")
	}
}

func TestMigrateFSDuplicateVersions(t *testing.T) {
	pool := openTestDB(t)
	fsys := fstest.MapFS{
		"m/0001_a.sql": {Data: []byte(`CREATE TABLE a (id INTEGER);`)},
		"m/0001_b.sql": {Data: []byte(`CREATE TABLE b (id INTEGER);`)},
	}
	if err := MigrateFS(pool, fsys, "m"); err == nil {
		t.Error("expected error for duplicate migration versions")
	}
}

func TestMigrateFSInvalidFilename(t *testing.T) {
	pool := openTestDB(t)
	fsys := fstest.MapFS{
		"m/bad.sql": {Data: []byte(`CREATE TABLE bad (id INTEGER);`)},
	}
	if err := MigrateFS(pool, fsys, "m"); err == nil {
		t.Error("expected error for invalid migration filename")
	}
}

func TestMigrateFSEmptyDir(t *testing.T) {
	pool := openTestDB(t)
	fsys := fstest.MapFS{
		"m/README.md": {Data: []byte(`none`)},
	}
	if err := MigrateFS(pool, fsys, "m"); err == nil {
		t.Error("expected error when no migration files exist")
	}
}

func TestParseMigrationName(t *testing.T) {
	version, name, err := parseMigrationName("0004_add_shares.sql")
	if err != nil {
		t.Fatalf("parseMigrationName: %v", err)
	}
	if version != 4 || name != "add_shares" {
		t.Errorf("got (%d, %q), want (4, add_shares)", version, name)
	}
	if _, _, err := parseMigrationName("nounder_score.sql"); err == nil {
		t.Error("expected error")
	}
	if _, _, err := parseMigrationName("0001_.sql"); err == nil {
		t.Error("expected error for missing description")
	}
}
