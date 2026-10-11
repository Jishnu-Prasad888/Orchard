package usersettings_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/Jishnu-Prasad888/Orchard/internal/db"
	"github.com/Jishnu-Prasad888/Orchard/internal/usersettings"
)

// newTestStore seeds a real users row (user_settings.user_id is a real
// foreign key, and this database runs with foreign_keys enforcement on).
func newTestStore(t *testing.T) (*usersettings.Store, string) {
	t.Helper()
	pool, err := db.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	seedUser(t, pool, "u1")
	return usersettings.NewStore(pool), "u1"
}

func seedUser(t *testing.T, pool *sql.DB, id string) {
	t.Helper()
	if _, err := pool.Exec(`INSERT INTO users
		(id, username, password_hash, role, is_enabled, created_at, updated_at)
		VALUES (?, ?, 'hash', 'user', 1, '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z')`,
		id, id+"-username"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
}

type readerPrefs struct {
	FontSize int    `json:"font_size"`
	Theme    string `json:"theme"`
}

func TestGetJSON_MissingReturnsNotOK(t *testing.T) {
	store, userID := newTestStore(t)
	var dest readerPrefs
	ok, err := store.GetJSON(context.Background(), userID, "reader.preferences", &dest)
	if err != nil {
		t.Fatalf("GetJSON: %v", err)
	}
	if ok {
		t.Error("expected ok=false for an unset key")
	}
}

func TestSetAndGetJSONRoundTrip(t *testing.T) {
	store, userID := newTestStore(t)
	ctx := context.Background()

	want := readerPrefs{FontSize: 18, Theme: "sepia"}
	if err := store.SetJSON(ctx, userID, "reader.preferences", want); err != nil {
		t.Fatalf("SetJSON: %v", err)
	}

	var got readerPrefs
	ok, err := store.GetJSON(ctx, userID, "reader.preferences", &got)
	if err != nil || !ok {
		t.Fatalf("GetJSON: ok=%v err=%v", ok, err)
	}
	if got != want {
		t.Errorf("got = %+v, want %+v", got, want)
	}
}

func TestSetJSON_UpsertsExistingValue(t *testing.T) {
	store, userID := newTestStore(t)
	ctx := context.Background()

	if err := store.SetJSON(ctx, userID, "reader.preferences", readerPrefs{FontSize: 14, Theme: "light"}); err != nil {
		t.Fatalf("SetJSON: %v", err)
	}
	if err := store.SetJSON(ctx, userID, "reader.preferences", readerPrefs{FontSize: 22, Theme: "dark"}); err != nil {
		t.Fatalf("SetJSON: %v", err)
	}

	var got readerPrefs
	if _, err := store.GetJSON(ctx, userID, "reader.preferences", &got); err != nil {
		t.Fatal(err)
	}
	if got != (readerPrefs{FontSize: 22, Theme: "dark"}) {
		t.Errorf("got = %+v, want the second write", got)
	}
}

func TestSettingsArePerUser(t *testing.T) {
	store, userID := newTestStore(t)
	ctx := context.Background()

	if err := store.SetJSON(ctx, userID, "reader.preferences", readerPrefs{FontSize: 18}); err != nil {
		t.Fatalf("SetJSON: %v", err)
	}

	var got readerPrefs
	ok, err := store.GetJSON(ctx, "other-user", "reader.preferences", &got)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("a second user must not see the first user's settings")
	}
}

func TestSettingsArePerKey(t *testing.T) {
	store, userID := newTestStore(t)
	ctx := context.Background()

	if err := store.SetJSON(ctx, userID, "reader.preferences", readerPrefs{FontSize: 18}); err != nil {
		t.Fatalf("SetJSON: %v", err)
	}

	var otherDest string
	ok, err := store.GetJSON(ctx, userID, "some.other.key", &otherDest)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("a different key must not see this value")
	}
}
