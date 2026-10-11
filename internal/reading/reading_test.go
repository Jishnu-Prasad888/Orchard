package reading_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Jishnu-Prasad888/Orchard/internal/librarydb"
	"github.com/Jishnu-Prasad888/Orchard/internal/reading"
)

func newTestStore(t *testing.T) (*reading.Store, func(fileID, relPath string)) {
	t.Helper()
	orchardDir := filepath.Join(t.TempDir(), ".orchard")
	if err := os.MkdirAll(orchardDir, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := librarydb.Open(orchardDir)
	if err != nil {
		t.Fatalf("open library db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	seed := func(fileID, relPath string) {
		if _, err := db.Exec(`INSERT INTO indexed_files
			(id, rel_path, size_bytes, mod_time, status, first_seen_at, last_seen_at, indexed_at)
			VALUES (?, ?, 1, '', 'present', '', '', '')`, fileID, relPath); err != nil {
			t.Fatalf("seed file: %v", err)
		}
	}
	return reading.NewStore(db), seed
}

func TestGet_NoStatusReturnsNotOK(t *testing.T) {
	store, seed := newTestStore(t)
	seed("f1", "book.epub")

	_, ok, err := store.Get(context.Background(), "u1", "f1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if ok {
		t.Error("expected ok=false when no status set")
	}
}

func TestSetAndGetRoundTrip(t *testing.T) {
	store, seed := newTestStore(t)
	seed("f1", "book.epub")
	ctx := context.Background()

	if err := store.Set(ctx, "u1", "f1", reading.StatusReading); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, ok, err := store.Get(ctx, "u1", "f1")
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if got != reading.StatusReading {
		t.Errorf("status = %q, want %q", got, reading.StatusReading)
	}
}

func TestSet_UpsertsExistingStatus(t *testing.T) {
	store, seed := newTestStore(t)
	seed("f1", "book.epub")
	ctx := context.Background()

	if err := store.Set(ctx, "u1", "f1", reading.StatusWantToRead); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := store.Set(ctx, "u1", "f1", reading.StatusCompleted); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, _, err := store.Get(ctx, "u1", "f1")
	if err != nil {
		t.Fatal(err)
	}
	if got != reading.StatusCompleted {
		t.Errorf("status = %q, want %q", got, reading.StatusCompleted)
	}
}

func TestStatusIsPerUser(t *testing.T) {
	store, seed := newTestStore(t)
	seed("f1", "book.epub")
	ctx := context.Background()

	if err := store.Set(ctx, "u1", "f1", reading.StatusReading); err != nil {
		t.Fatalf("Set u1: %v", err)
	}
	_, ok, err := store.Get(ctx, "u2", "f1")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("a second user must not see the first user's reading status")
	}
}

func TestClear(t *testing.T) {
	store, seed := newTestStore(t)
	seed("f1", "book.epub")
	ctx := context.Background()

	if err := store.Set(ctx, "u1", "f1", reading.StatusOnHold); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := store.Clear(ctx, "u1", "f1"); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	_, ok, err := store.Get(ctx, "u1", "f1")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("expected no status after Clear")
	}
}

func TestClear_MissingStatusIsNotAnError(t *testing.T) {
	store, seed := newTestStore(t)
	seed("f1", "book.epub")

	if err := store.Clear(context.Background(), "u1", "f1"); err != nil {
		t.Errorf("Clear on unset status: %v", err)
	}
}

func TestValid(t *testing.T) {
	cases := map[reading.Status]bool{
		reading.StatusWantToRead: true,
		reading.StatusReading:    true,
		reading.StatusCompleted:  true,
		reading.StatusOnHold:     true,
		"bogus":                  false,
		"":                       false,
	}
	for status, want := range cases {
		if got := status.Valid(); got != want {
			t.Errorf("Valid(%q) = %v, want %v", status, got, want)
		}
	}
}
