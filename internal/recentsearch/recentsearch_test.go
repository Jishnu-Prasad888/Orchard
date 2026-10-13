package recentsearch_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Jishnu-Prasad888/Orchard/internal/librarydb"
	"github.com/Jishnu-Prasad888/Orchard/internal/recentsearch"
)

func newTestStore(t *testing.T) *recentsearch.Store {
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
	return recentsearch.NewStore(db)
}

func TestRecordAndList(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	if err := store.Record(ctx, "u1", "dune"); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := store.Record(ctx, "u1", "arrakis"); err != nil {
		t.Fatalf("Record: %v", err)
	}

	list, err := store.List(ctx, "u1", 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 || list[0].Query != "arrakis" || list[1].Query != "dune" {
		t.Errorf("List = %+v, want [arrakis, dune] newest first", list)
	}
}

func TestRecordIgnoresBlank(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	if err := store.Record(ctx, "u1", "   "); err != nil {
		t.Fatalf("Record: %v", err)
	}
	list, err := store.List(ctx, "u1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Errorf("List = %+v, want empty for a blank query", list)
	}
}

func TestRecordRepeatedQueryBumpsNotDuplicates(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	if err := store.Record(ctx, "u1", "dune"); err != nil {
		t.Fatal(err)
	}
	if err := store.Record(ctx, "u1", "arrakis"); err != nil {
		t.Fatal(err)
	}
	if err := store.Record(ctx, "u1", "dune"); err != nil {
		t.Fatal(err)
	}

	list, err := store.List(ctx, "u1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("len = %d, want 2 (no duplicate row for a repeated query)", len(list))
	}
	if list[0].Query != "dune" {
		t.Errorf("most recent = %q, want dune (bumped by the repeat)", list[0].Query)
	}
}

func TestRecordScopedPerUser(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	if err := store.Record(ctx, "u1", "mine"); err != nil {
		t.Fatal(err)
	}
	if err := store.Record(ctx, "u2", "theirs"); err != nil {
		t.Fatal(err)
	}

	list, err := store.List(ctx, "u1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Query != "mine" {
		t.Errorf("List = %+v, want only u1's own search", list)
	}
}

func TestRecordTrimsToMaxPerUser(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	for i := range recentsearch.MaxPerUser + 5 {
		if err := store.Record(ctx, "u1", fmt.Sprintf("query-%d", i)); err != nil {
			t.Fatal(err)
		}
	}

	list, err := store.List(ctx, "u1", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != recentsearch.MaxPerUser {
		t.Errorf("len = %d, want %d (trimmed)", len(list), recentsearch.MaxPerUser)
	}
	// The oldest queries should have been trimmed, the newest kept.
	newest := fmt.Sprintf("query-%d", recentsearch.MaxPerUser+4)
	if list[0].Query != newest {
		t.Errorf("newest = %q, want %q", list[0].Query, newest)
	}
}

func TestForget(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	if err := store.Record(ctx, "u1", "dune"); err != nil {
		t.Fatal(err)
	}
	if err := store.Forget(ctx, "u1", "dune"); err != nil {
		t.Fatalf("Forget: %v", err)
	}
	list, err := store.List(ctx, "u1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Errorf("List = %+v, want empty after Forget", list)
	}
}

func TestClear(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	if err := store.Record(ctx, "u1", "dune"); err != nil {
		t.Fatal(err)
	}
	if err := store.Record(ctx, "u1", "arrakis"); err != nil {
		t.Fatal(err)
	}
	if err := store.Clear(ctx, "u1"); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	list, err := store.List(ctx, "u1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Errorf("List = %+v, want empty after Clear", list)
	}
}
