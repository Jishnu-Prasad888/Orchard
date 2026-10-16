package librarydb_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/Jishnu-Prasad888/Orchard/internal/librarydb"
)

func openMembersDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := filepath.Join(t.TempDir(), ".orchard")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := librarydb.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	mustExec(t, db, `INSERT INTO indexed_files (id, rel_path, size_bytes, mod_time, first_seen_at, last_seen_at, indexed_at)
		VALUES ('f1', 'book.pdf', 1, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z',
		        '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`)
	return db
}

func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

// writeReadingData gives userID a bookmark, a quote (which is also indexed
// for content search), and a favorite.
func writeReadingData(t *testing.T, db *sql.DB, userID string) {
	t.Helper()
	mustExec(t, db, `INSERT INTO bookmarks (id, user_id, file_id, location, created_at)
		VALUES (?, ?, 'f1', 'page:3', '2026-01-01T00:00:00Z')`, "b-"+userID, userID)
	mustExec(t, db, `INSERT INTO quotes (id, user_id, file_id, text, created_at, updated_at)
		VALUES (?, ?, 'f1', 'a line', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`, "q-"+userID, userID)
	mustExec(t, db, `INSERT INTO fts_content (source_type, source_id, file_id, user_id, text)
		VALUES ('quote', ?, 'f1', ?, 'a line')`, "q-"+userID, userID)
	mustExec(t, db, `INSERT INTO favorites (user_id, file_id, created_at)
		VALUES (?, 'f1', '2026-01-01T00:00:00Z')`, userID)
}

func countFor(t *testing.T, db *sql.DB, table, userID string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM `+table+` WHERE user_id = ?`, userID).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func noAccounts(context.Context, string) (bool, error) { return false, nil }

func TestReconnectMember_MovesDataOfSameUsernameFromAnotherInstall(t *testing.T) {
	ctx := context.Background()
	db := openMembersDB(t)

	// The old install: alice reads, and the library learns who she is.
	if _, err := librarydb.ReconnectMember(ctx, db, "old-alice", "alice", librarydb.AccountLookup{}); err != nil {
		t.Fatal(err)
	}
	writeReadingData(t, db, "old-alice")
	if err := librarydb.SetMemberSetting(ctx, db, "old-alice", "reader.preferences", map[string]string{"theme": "sepia"}); err != nil {
		t.Fatal(err)
	}

	// A fresh install: alice signs up again and gets a new id.
	n, err := librarydb.ReconnectMember(ctx, db, "new-alice", "Alice",
		librarydb.AccountLookup{Exists: noAccounts})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("reconnected %d ids, want 1", n)
	}
	for _, table := range []string{"bookmarks", "quotes", "fts_content", "favorites", "member_settings"} {
		if got := countFor(t, db, table, "new-alice"); got != 1 {
			t.Errorf("%s rows for new id = %d, want 1", table, got)
		}
		if got := countFor(t, db, table, "old-alice"); got != 0 {
			t.Errorf("%s rows left on old id = %d, want 0", table, got)
		}
	}
	var prefs map[string]string
	if ok, err := librarydb.GetMemberSetting(ctx, db, "new-alice", "reader.preferences", &prefs); err != nil || !ok || prefs["theme"] != "sepia" {
		t.Errorf("member setting = %v (ok=%v, err=%v), want sepia", prefs, ok, err)
	}

	// Doing it again is a no-op.
	if n, err := librarydb.ReconnectMember(ctx, db, "new-alice", "alice",
		librarydb.AccountLookup{Exists: noAccounts}); err != nil || n != 0 {
		t.Errorf("second reconnect = %d, %v; want 0, nil", n, err)
	}
}

func TestReconnectMember_KeepsRowsTheNewIDAlreadyHas(t *testing.T) {
	ctx := context.Background()
	db := openMembersDB(t)
	if _, err := librarydb.ReconnectMember(ctx, db, "old", "alice", librarydb.AccountLookup{}); err != nil {
		t.Fatal(err)
	}
	writeReadingData(t, db, "old")
	// The new id starred the same book before reconnecting.
	mustExec(t, db, `INSERT INTO favorites (user_id, file_id, created_at) VALUES ('new', 'f1', '2026-02-01T00:00:00Z')`)

	if _, err := librarydb.ReconnectMember(ctx, db, "new", "alice", librarydb.AccountLookup{Exists: noAccounts}); err != nil {
		t.Fatal(err)
	}
	if got := countFor(t, db, "favorites", "new"); got != 1 {
		t.Errorf("favorites for new id = %d, want 1", got)
	}
	if got := countFor(t, db, "favorites", "old"); got != 0 {
		t.Errorf("favorites left on old id = %d, want 0", got)
	}
}

func TestReconnectMember_NeverTakesALiveAccountsData(t *testing.T) {
	ctx := context.Background()
	db := openMembersDB(t)
	if _, err := librarydb.ReconnectMember(ctx, db, "bob-id", "alice", librarydb.AccountLookup{}); err != nil {
		t.Fatal(err)
	}
	writeReadingData(t, db, "bob-id")

	// "bob-id" is still an account on this install (it was renamed), so a
	// newcomer who picks the old username gets nothing of it.
	live := func(_ context.Context, id string) (bool, error) { return id == "bob-id", nil }
	n, err := librarydb.ReconnectMember(ctx, db, "newcomer", "alice", librarydb.AccountLookup{Exists: live})
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 || countFor(t, db, "bookmarks", "bob-id") != 1 {
		t.Errorf("reconnected %d; bob's bookmarks = %d — a live account's data moved", n, countFor(t, db, "bookmarks", "bob-id"))
	}
}

func TestReconnectMember_ClaimsUnrecordedDataOnlyOnASingleAccountInstall(t *testing.T) {
	ctx := context.Background()

	// Data written before library_members existed has no username.
	db := openMembersDB(t)
	writeReadingData(t, db, "legacy")
	if n, err := librarydb.ReconnectMember(ctx, db, "me", "me", librarydb.AccountLookup{Exists: noAccounts}); err != nil || n != 0 {
		t.Fatalf("multi-account install reconnected %d, %v; want 0", n, err)
	}
	if countFor(t, db, "bookmarks", "legacy") != 1 {
		t.Fatal("legacy data moved without a sole account")
	}

	db = openMembersDB(t)
	writeReadingData(t, db, "legacy")
	n, err := librarydb.ReconnectMember(ctx, db, "me", "me",
		librarydb.AccountLookup{Exists: noAccounts, SoleAccount: true})
	if err != nil || n != 1 {
		t.Fatalf("sole-account reconnect = %d, %v; want 1", n, err)
	}
	if countFor(t, db, "bookmarks", "me") != 1 {
		t.Error("legacy bookmark not reconnected")
	}

	// With two unrecorded ids there is no telling whose is whose.
	db = openMembersDB(t)
	writeReadingData(t, db, "legacy-1")
	mustExec(t, db, `INSERT INTO bookmarks (id, user_id, file_id, location, created_at)
		VALUES ('b2', 'legacy-2', 'f1', 'page:9', '2026-01-01T00:00:00Z')`)
	if n, err := librarydb.ReconnectMember(ctx, db, "me", "me",
		librarydb.AccountLookup{Exists: noAccounts, SoleAccount: true}); err != nil || n != 0 {
		t.Errorf("ambiguous reconnect = %d, %v; want 0", n, err)
	}
}
