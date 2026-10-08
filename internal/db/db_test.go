package db

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDSN(t *testing.T) {
	got := dsn("/var/lib/cairn/cairn.db")

	if !strings.HasPrefix(got, "file:/var/lib/cairn/cairn.db?") {
		t.Fatalf("dsn prefix = %q", got)
	}
	for _, pragma := range []string{
		"_pragma=foreign_keys(1)",
		"_pragma=busy_timeout(5000)",
		"_pragma=synchronous(NORMAL)",
		"_pragma=journal_mode(WAL)",
	} {
		if !strings.Contains(got, pragma) {
			t.Errorf("dsn missing %q: %s", pragma, got)
		}
	}
}

func TestOpenCreatesDatabase(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "cairn.db")

	pool, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = pool.Close() }()

	var journal string
	if err := pool.QueryRow(`PRAGMA journal_mode`).Scan(&journal); err != nil {
		t.Fatalf("query journal_mode: %v", err)
	}
	if !strings.EqualFold(journal, "wal") {
		t.Errorf("journal_mode = %q, want wal", journal)
	}

	var fk int
	if err := pool.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil {
		t.Fatalf("query foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Errorf("foreign_keys = %d, want 1", fk)
	}
}

func TestOpenInvalidPath(t *testing.T) {
	// A path whose parent is an existing regular file cannot be created.
	dir := t.TempDir()
	file := filepath.Join(dir, "blocker")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(filepath.Join(file, "cairn.db")); err == nil {
		t.Error("expected error when database directory cannot be created")
	}
}
