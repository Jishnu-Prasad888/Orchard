package serversettings_test

import (
	"context"
	"testing"

	"github.com/Jishnu-Prasad888/Orchard/internal/db"
	"github.com/Jishnu-Prasad888/Orchard/internal/serversettings"
)

func newTestStore(t *testing.T) *serversettings.Store {
	t.Helper()
	pool, err := db.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return serversettings.NewStore(pool)
}

func TestGetBool_DefaultWhenUnset(t *testing.T) {
	store := newTestStore(t)
	got, err := store.GetBool(context.Background(), "some.key", true)
	if err != nil {
		t.Fatalf("GetBool: %v", err)
	}
	if !got {
		t.Error("expected default true for an unset key")
	}
}

func TestSetAndGetBoolRoundTrip(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	if err := store.SetBool(ctx, "signup.requests_enabled", true); err != nil {
		t.Fatalf("SetBool: %v", err)
	}
	got, err := store.GetBool(ctx, "signup.requests_enabled", false)
	if err != nil {
		t.Fatalf("GetBool: %v", err)
	}
	if !got {
		t.Error("expected true after SetBool(true)")
	}

	if err := store.SetBool(ctx, "signup.requests_enabled", false); err != nil {
		t.Fatalf("SetBool: %v", err)
	}
	got, err = store.GetBool(ctx, "signup.requests_enabled", true)
	if err != nil {
		t.Fatalf("GetBool: %v", err)
	}
	if got {
		t.Error("expected false after SetBool(false)")
	}
}
