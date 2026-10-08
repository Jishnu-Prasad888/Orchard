package library

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/Jishnu-Prasad888/Cairn/internal/audit"
	"github.com/Jishnu-Prasad888/Cairn/internal/crypto"
	"github.com/Jishnu-Prasad888/Cairn/internal/db"
)

func newTestManager(t *testing.T) (*Manager, *sql.DB) {
	t.Helper()
	pool, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewManager(pool, logger, audit.New(pool, logger), crypto.NewKeys("")), pool
}

func TestCreateRegistersAndWritesMetadata(t *testing.T) {
	m, pool := newTestManager(t)
	root := filepath.Join(t.TempDir(), "photos")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}

	lib, mode, err := m.Register(context.Background(), root, "Vacation Photos")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if mode != "created" {
		t.Errorf("mode = %q, want created", mode)
	}
	if lib == nil || lib.ID == "" || lib.Name != "Vacation Photos" {
		t.Fatalf("lib = %+v", lib)
	}
	if lib.Status != StatusOnline {
		t.Errorf("status = %q, want online", lib.Status)
	}
	if !hasMetadata(root) {
		t.Error("metadata not created on disk")
	}

	// Registrar row exists.
	var got Library
	if err := pool.QueryRow(`SELECT id, name, root FROM libraries WHERE id = ?`, lib.ID).
		Scan(&got.ID, &got.Name, &got.Root); err != nil {
		t.Fatalf("registrar row: %v", err)
	}

	// Duplicate registration fails.
	if _, _, err := m.Register(context.Background(), root, "dupe"); err != ErrExists {
		t.Errorf("duplicate Register err = %v, want ErrExists", err)
	}
}

func TestCreateRejectsOverlappingAndInvalidRoots(t *testing.T) {
	m, _ := newTestManager(t)
	base := t.TempDir()
	a := filepath.Join(base, "a")
	b := filepath.Join(base, "b")
	for _, d := range []string{a, b} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := m.Register(context.Background(), a, "A"); err != nil {
		t.Fatalf("register a: %v", err)
	}
	// Direct child of an existing root overlaps.
	if _, _, err := m.Register(context.Background(), filepath.Join(a, "child"), "nested"); err == nil {
		t.Error("expected overlap rejection for nested root")
	}
	// Parent of an existing root overlaps.
	if _, _, err := m.Register(context.Background(), base, "parent"); err == nil {
		t.Error("expected overlap rejection for parent root")
	}
	// Sibling is fine.
	if _, _, err := m.Register(context.Background(), b, "B"); err != nil {
		t.Errorf("register sibling: %v", err)
	}

	for _, bad := range []struct{ path, name string }{
		{"relative/path", ""},
		{"", ""},
	} {
		if _, _, err := m.Register(context.Background(), bad.path, bad.name); err == nil {
			t.Errorf("Register(%q) expected error", bad.path)
		}
	}
}

func TestRegisterCreatesParentsOnDemand(t *testing.T) {
	m, _ := newTestManager(t)
	// A nested, not-yet-existing root is created by registration.
	root := filepath.Join(t.TempDir(), "deep", "nested", "photos")
	lib, _, err := m.Register(context.Background(), root, "Deep")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(root)); err != nil {
		t.Fatalf("parent dir not created: %v", err)
	}
	if lib.Root != filepath.Clean(root) {
		t.Errorf("root = %q, want %q", lib.Root, filepath.Clean(root))
	}
}

func TestAdoptLoadsExistingMetadata(t *testing.T) {
	m, _ := newTestManager(t)
	root := t.TempDir()

	// Pre-create metadata as if created on another machine.
	ident := createIdentityMust(t, root)
	lib, _, err := m.Register(context.Background(), root, "adopted name")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if lib.ID != ident.ID {
		t.Errorf("adopted id = %q, want identity id %q", lib.ID, ident.ID)
	}
	if lib.Name != "adopted name" {
		t.Errorf("name = %q, want registered name", lib.Name)
	}
}

func TestReconnectAtNewPathRebindsIdentity(t *testing.T) {
	m, _ := newTestManager(t)
	root1 := t.TempDir()
	lib, _, err := m.Register(context.Background(), root1, "media")
	if err != nil {
		t.Fatal(err)
	}
	_ = os.RemoveAll(root1) // "disk removed"

	// Same metadata present now at a different path (disk remounted elsewhere).
	// Simulate re-creating the library's own identity at the new mount point.
	root2 := t.TempDir()
	_ = os.RemoveAll(filepath.Join(root2, ".cairn"))
	_ = copyDir(root1, root2) // no-op for nonexistent source
	// Write the original library identity at root2 so Refresh can verify it.
	dir := metadataDir(root2)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	origIdent, err := m.Get(context.Background(), lib.ID)
	if err != nil {
		t.Fatalf("get original lib: %v", err)
	}
	if err := writeIdentityFile(dir, &identity{
		ID:            origIdent.ID,
		SchemaVersion: metadataSchemaVersion,
		Name:          origIdent.Name,
		CreatedAt:     origIdent.CreatedAt,
	}, crypto.NewKeys("")); err != nil {
		t.Fatalf("write identity to root2: %v", err)
	}

	refreshed, err := m.Refresh(context.Background(), lib.ID, root2)
	if err != nil {
		t.Fatalf("Refresh at new path: %v", err)
	}
	if refreshed.Root != root2 {
		t.Errorf("root = %q, want %q", refreshed.Root, root2)
	}
	if refreshed.ID != lib.ID {
		t.Errorf("id = %q, want %q", refreshed.ID, lib.ID)
	}
	if refreshed.Status != StatusOnline {
		t.Errorf("status = %q, want online", refreshed.Status)
	}

	// Registrar no longer references the old root and is not duplicated.
	got, err := m.Get(context.Background(), lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Root != root2 || got.VolumeID == "" {
		t.Errorf("reconnected = %+v", got)
	}
	var n int
	if err := m.db.QueryRow(`SELECT COUNT(*) FROM libraries`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("libraries rows = %d, want 1 (no duplicate)", n)
	}
}

func TestReconnectRejectsDifferentIdentity(t *testing.T) {
	m, _ := newTestManager(t)
	root1 := t.TempDir()
	lib, _, err := m.Register(context.Background(), root1, "one")
	if err != nil {
		t.Fatal(err)
	}
	root2 := t.TempDir()
	_ = createIdentityMust(t, root2) // different id

	if _, err := m.Refresh(context.Background(), lib.ID, root2); err == nil {
		t.Error("expected identity mismatch error")
	}
}

func TestRefreshFlipsOfflineAndBack(t *testing.T) {
	m, _ := newTestManager(t)
	root := t.TempDir()
	lib, _, err := m.Register(context.Background(), root, "flaky")
	if err != nil {
		t.Fatal(err)
	}

	_ = os.RemoveAll(root)
	got, err := m.Refresh(context.Background(), lib.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusOffline {
		t.Errorf("after removal status = %q, want offline", got.Status)
	}

	// Recreate the directory to simulate reconnection on the same volume/path.
	_ = os.MkdirAll(root, 0o755)
	got, err = m.Refresh(context.Background(), lib.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusOnline {
		t.Errorf("after reconnect status = %q, want online", got.Status)
	}
}

func TestListAndRefreshAll(t *testing.T) {
	m, _ := newTestManager(t)
	roots := []string{t.TempDir(), t.TempDir()}
	for i, root := range roots {
		if _, _, err := m.Register(context.Background(), root, "lib-"+string(rune('a'+i))); err != nil {
			t.Fatal(err)
		}
	}

	all, err := m.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("len = %d, want 2", len(all))
	}
	if all[0].Name != "lib-a" || all[1].Name != "lib-b" {
		t.Errorf("order = %q, %q", all[0].Name, all[1].Name)
	}

	_ = os.RemoveAll(roots[1])
	if err := m.RefreshAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	all, _ = m.List(context.Background())
	for _, lib := range all {
		want := StatusOnline
		if lib.Root == roots[1] {
			want = StatusOffline
		}
		if lib.Status != want {
			t.Errorf("%s status = %q, want %q", lib.Name, lib.Status, want)
		}
	}
}

func TestRemoveKeepsMetadata(t *testing.T) {
	m, _ := newTestManager(t)
	root := t.TempDir()
	lib, _, err := m.Register(context.Background(), root, "gone soon")
	if err != nil {
		t.Fatal(err)
	}

	if err := m.Remove(context.Background(), lib.ID); err != nil {
		t.Fatal(err)
	}
	if !hasMetadata(root) {
		t.Error("metadata removed from disk")
	}
	if _, err := m.Get(context.Background(), lib.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after remove err = %v, want ErrNotFound", err)
	}
}

func TestProbeReportsCandidates(t *testing.T) {
	m, _ := newTestManager(t)
	root := t.TempDir()
	if _, _, err := m.Register(context.Background(), root, "probed"); err != nil {
		t.Fatal(err)
	}

	probe, err := m.Probe(context.Background(), filepath.Join(root, "sub", "dir"))
	if err != nil {
		t.Fatal(err)
	}
	if probe.HasMetadata {
		t.Error("nested candidate unexpectedly has metadata")
	}
	if probe.Registered {
		t.Error("nested candidate unexpectedly registered")
	}
}

// copyDir recursively copies src to dst, preserving directory trees. It
// tolerates a missing src (a no-op), which the reconnect tests rely on when
// simulating a disk that has been removed.
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
}
