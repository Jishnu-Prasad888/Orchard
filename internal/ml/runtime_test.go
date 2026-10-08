package ml

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"testing"

	_ "modernc.org/sqlite"
)

func newTestRuntime(t *testing.T, def bool) (*Runtime, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE server_settings (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	rt := NewRuntime(db, logger, NewManager(logger, Config{}, nil), NewFaceManager(logger, FaceConfig{}, nil), true,
		func(context.Context) ([]Target, error) { return nil, nil })
	if err := rt.Load(context.Background(), def); err != nil {
		t.Fatal(err)
	}
	return rt, db
}

func TestRuntimeSwitchPersistsAndDrivesBothManagers(t *testing.T) {
	rt, db := newTestRuntime(t, false)
	if rt.Enabled() {
		t.Fatal("ML should start off")
	}
	if err := rt.Set(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if !rt.sim.Enabled() || !rt.faces.Enabled() {
		t.Fatal("turning the switch on must enable similarity and people")
	}

	// A fresh runtime on the same database restores the stored choice over the default.
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	again := NewRuntime(db, logger, NewManager(logger, Config{}, nil), NewFaceManager(logger, FaceConfig{}, nil), true,
		func(context.Context) ([]Target, error) { return nil, nil })
	if err := again.Load(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if !again.Enabled() {
		t.Fatal("stored switch should win over the default")
	}

	if err := rt.Set(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if rt.sim.Enabled() || rt.faces.Enabled() {
		t.Fatal("turning the switch off must stop both")
	}
}
