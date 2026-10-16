package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/Jishnu-Prasad888/Orchard/internal/librarydb"
)

// A library carried over from another Orchard install holds its reading data
// under that install's (random) user ids. Opening it here, as the same
// username, must bring that data back.
func TestLibraryDataFollowsTheUsernameAcrossInstalls(t *testing.T) {
	_, client, libID, libRoot := newSearchTestServer(t)
	if err := seedLibraryFile(t, libRoot, "f1", "book.pdf", 10); err != nil {
		t.Fatal(err)
	}

	// What the old install left behind: admin's bookmark and reader
	// preferences, under admin's old id.
	ldb, err := librarydb.Open(filepath.Join(libRoot, ".orchard"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := librarydb.ReconnectMember(ctx, ldb, "old-admin-id", "admin", librarydb.AccountLookup{}); err != nil {
		t.Fatal(err)
	}
	if _, err := ldb.Exec(`INSERT INTO bookmarks (id, user_id, file_id, location, created_at)
		VALUES ('b1', 'old-admin-id', 'f1', 'page:7', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	oldPrefs := defaultReaderPreferences
	oldPrefs.Theme = "sepia"
	oldPrefs.FontSize = 26
	if err := librarydb.SetMemberSetting(ctx, ldb, "old-admin-id", readerPreferencesKey, oldPrefs); err != nil {
		t.Fatal(err)
	}
	_ = ldb.Close()

	// This install's first request touching libraries reconnects admin.
	if rec := client.roundTrip(t, http.MethodGet, "/api/v1/libraries", ""); rec.Code != http.StatusOK {
		t.Fatalf("list libraries status = %d", rec.Code)
	}

	rec := client.roundTrip(t, http.MethodGet, "/api/v1/libraries/"+libID+"/files/f1/bookmarks", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("bookmarks status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var bookmarks struct {
		Bookmarks []struct {
			ID       string `json:"id"`
			Location string `json:"location"`
		} `json:"bookmarks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &bookmarks); err != nil {
		t.Fatal(err)
	}
	if len(bookmarks.Bookmarks) != 1 || bookmarks.Bookmarks[0].Location != "page:7" {
		t.Errorf("bookmarks = %+v, want the old install's page:7 bookmark", bookmarks.Bookmarks)
	}

	rec = client.roundTrip(t, http.MethodGet, "/api/v1/settings/reader?library="+libID, "")
	var prefs struct {
		Preferences ReaderPreferences `json:"preferences"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &prefs); err != nil {
		t.Fatal(err)
	}
	if prefs.Preferences != oldPrefs {
		t.Errorf("reader preferences = %+v, want the library's %+v", prefs.Preferences, oldPrefs)
	}
}

// Saving a preference with a library named keeps a copy in that library.
func TestReaderPreferencesAreSavedIntoTheLibrary(t *testing.T) {
	_, client, libID, libRoot := newSearchTestServer(t)
	want := defaultReaderPreferences
	want.FontSize = 30
	if rec := client.do(t, http.MethodPut, "/api/v1/settings/reader?library="+libID, want); rec.Code != http.StatusOK {
		t.Fatalf("save status = %d, body=%s", rec.Code, rec.Body.String())
	}

	ldb, err := librarydb.Open(filepath.Join(libRoot, ".orchard"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ldb.Close() }()
	var userID string
	if err := ldb.QueryRow(`SELECT user_id FROM library_members WHERE username = 'admin'`).Scan(&userID); err != nil {
		t.Fatalf("admin not recorded as a library member: %v", err)
	}
	var got ReaderPreferences
	if ok, err := librarydb.GetMemberSetting(context.Background(), ldb, userID, readerPreferencesKey, &got); err != nil || !ok {
		t.Fatalf("library copy missing: ok=%v err=%v", ok, err)
	}
	if got != want {
		t.Errorf("library copy = %+v, want %+v", got, want)
	}
}
