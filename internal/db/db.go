// Package db manages the server-level SQLite database.
//
// It owns connection handling, pragma configuration, and schema migrations.
// Library-level databases are managed separately by the storage layer.
package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // registers the pure-Go "sqlite" database/sql driver
)

// Driver is the database/sql driver name used for all Cairn SQLite databases.
const Driver = "sqlite"

// Open opens (creating if necessary) the SQLite database at path and
// configures pragmas that apply to every pooled connection.
//
// The file is created with owner-only permissions. The server process is the
// only writer of any Cairn database.
func Open(path string) (*sql.DB, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
	}

	pool, err := sql.Open(Driver, dsn(path))
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	pool.SetMaxOpenConns(1)
	pool.SetMaxIdleConns(1)
	pool.SetConnMaxLifetime(0)
	pool.SetConnMaxIdleTime(0)

	if err := pool.Ping(); err != nil {
		_ = pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	// journal_mode is persistent (stored in the database file), so executing
	// it once here guarantees WAL even if a future driver drops the pragma
	// parameter. Other pragmas are per-connection and are applied via DSN.
	if _, err := pool.Exec(`PRAGMA journal_mode = WAL`); err != nil {
		_ = pool.Close()
		return nil, fmt.Errorf("enable WAL journal mode: %w", err)
	}

	return pool, nil
}

// dsn builds a connection string with per-connection pragmas. The driver
// applies pragma settings each time it opens a connection, keeping every
// pooled connection configured identically.
func dsn(path string) string {
	pragmas := []string{
		"foreign_keys(1)",
		"busy_timeout(5000)",
		"synchronous(NORMAL)",
		"journal_mode(WAL)",
	}
	u := "file:" + filepath.ToSlash(path) + "?"
	for i, p := range pragmas {
		if i > 0 {
			u += "&"
		}
		u += "_pragma=" + p
	}
	return u
}
