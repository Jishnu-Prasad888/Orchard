package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/Jishnu-Prasad888/Cairn/internal/audit"
	"github.com/Jishnu-Prasad888/Cairn/internal/auth"
	"github.com/Jishnu-Prasad888/Cairn/internal/authz"
	"github.com/Jishnu-Prasad888/Cairn/internal/crypto"
	"github.com/Jishnu-Prasad888/Cairn/internal/db"
	"github.com/Jishnu-Prasad888/Cairn/internal/library"
)

// newLibraryListServer wires a server with two registered libraries so tests
// can prove that listing is filtered per caller rather than per role alone. It
// returns the admin client, the two libraries, and a helper that signs in a
// freshly created member account.
func newLibraryListServer(t *testing.T) (*testClient, []library.Library, func() *testClient) {
	t.Helper()

	tmpDir := t.TempDir()
	pool, err := db.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	auditSvc := audit.New(pool, logger)
	authSvc := auth.NewService(pool, logger, auditSvc)
	authzSvc := authz.NewService(pool, logger, nil)
	libManager := library.NewManager(pool, logger, auditSvc, crypto.NewKeys(""))

	libs := make([]library.Library, 0, 2)
	for _, name := range []string{"Alpha", "Bravo"} {
		root := filepath.Join(tmpDir, name)
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		lib, _, err := libManager.Register(context.Background(), root, name)
		if err != nil {
			t.Fatalf("register %s: %v", name, err)
		}
		libs = append(libs, *lib)
	}

	handler := New(Dependencies{
		Logger:    logger,
		DB:        pool,
		Auth:      authSvc,
		Authz:     authzSvc,
		Libraries: libManager,
	}).Handler()

	admin := &testClient{handler: handler}
	admin.roundTrip(t, http.MethodPost, "/api/v1/auth/bootstrap",
		`{"username":"admin","password":"correct-horse-battery"}`)

	// member creates a non-admin account and returns a signed-in client for it,
	// along with the new account id.
	var memberID string
	member := func() *testClient {
		t.Helper()
		if memberID == "" {
			rec := admin.do(t, http.MethodPost, "/api/v1/users", map[string]string{
				"username": "member", "password": "member-password",
			})
			if rec.Code != http.StatusCreated {
				t.Fatalf("create member = %d, body=%s", rec.Code, rec.Body.String())
			}
			var created struct {
				User struct {
					ID string `json:"id"`
				} `json:"user"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
				t.Fatalf("unmarshal member: %v", err)
			}
			memberID = created.User.ID
		}
		c := &testClient{handler: handler}
		c.roundTrip(t, http.MethodPost, "/api/v1/auth/login",
			`{"username":"member","password":"member-password"}`)
		return c
	}

	return admin, libs, member
}

// The library fields the web client renders. Kept as a named type so the
// assertions below read clearly and stay in sync with docs/api.md.
type libraryListEntry struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Root          string `json:"root"`
	Status        string `json:"status"`
	VolumeID      string `json:"volume_id"`
	SchemaVersion int    `json:"schema_version"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

type libraryListResponse struct {
	Libraries []libraryListEntry `json:"libraries"`
}

// listLibraries performs the listing and asserts that the documented library
// fields (root, status) are actually populated — the web client depends on them
// to show where a library lives and whether its storage is currently reachable.
func listLibraries(t *testing.T, c *testClient) []libraryListEntry {
	t.Helper()
	rec := c.do(t, http.MethodGet, "/api/v1/libraries", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list libraries = %d, body=%s", rec.Code, rec.Body.String())
	}
	var resp libraryListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal libraries: %v", err)
	}
	for _, l := range resp.Libraries {
		if l.Status == "" {
			t.Errorf("library %q has no status; the web client cannot show online/offline", l.Name)
		}
		if l.Root == "" {
			t.Errorf("library %q has no root; the web client cannot show its path", l.Name)
		}
	}
	return resp.Libraries
}

func idsOf(libs []libraryListEntry) []string {
	out := make([]string, 0, len(libs))
	for _, l := range libs {
		out = append(out, l.ID)
	}
	return out
}

// userIDByName looks up an account id, so tests can grant capabilities without
// threading ids through every helper.
func userIDByName(t *testing.T, admin *testClient, username string) string {
	t.Helper()
	rec := admin.do(t, http.MethodGet, "/api/v1/users", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list users = %d", rec.Code)
	}
	var resp struct {
		Users []struct {
			ID       string `json:"id"`
			Username string `json:"username"`
		} `json:"users"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal users: %v", err)
	}
	for _, u := range resp.Users {
		if u.Username == username {
			return u.ID
		}
	}
	t.Fatalf("user %q not found", username)
	return ""
}

func TestListLibraries_AdminSeesEveryLibrary(t *testing.T) {
	admin, libs, _ := newLibraryListServer(t)

	seen := idsOf(listLibraries(t, admin))
	if len(seen) != len(libs) {
		t.Fatalf("admin saw %d libraries, want %d (%v)", len(seen), len(libs), seen)
	}
	for _, lib := range libs {
		found := false
		for _, id := range seen {
			if id == lib.ID {
				found = true
			}
		}
		if !found {
			t.Errorf("admin cannot see library %q", lib.Name)
		}
	}
}

// A member with no grants gets an empty list, not a 403: the caller may list,
// there is simply nothing they are allowed to see. This is what makes the
// content pages usable without an administrator in the loop.
func TestListLibraries_MemberWithoutGrantsSeesEmptyList(t *testing.T) {
	_, _, member := newLibraryListServer(t)

	if seen := idsOf(listLibraries(t, member())); len(seen) != 0 {
		t.Fatalf("member with no grants saw %v, want none", seen)
	}
}

func TestListLibraries_MemberWithGrantSeesOnlyGrantedLibrary(t *testing.T) {
	admin, libs, memberFn := newLibraryListServer(t)
	member := memberFn()

	grantCap(t, admin, libs[0].ID, userIDByName(t, admin, "member"),
		authz.LibraryKey(libs[0].ID), []string{"read"})

	seen := idsOf(listLibraries(t, member))
	if len(seen) != 1 || seen[0] != libs[0].ID {
		t.Fatalf("member saw %v, want only %s", seen, libs[0].ID)
	}
}

// A grant scoped to a subfolder does not by itself make the library itself
// listable: the listing answers "which libraries may I open", not "which files
// may I see", and a library with no library-scope read is not openable.
func TestListLibraries_FolderScopedGrantDoesNotExposeLibrary(t *testing.T) {
	admin, libs, memberFn := newLibraryListServer(t)
	member := memberFn()

	grantCap(t, admin, libs[0].ID, userIDByName(t, admin, "member"),
		authz.FolderKey(libs[0].ID, "photos"), []string{"read"})

	if seen := idsOf(listLibraries(t, member)); len(seen) != 0 {
		t.Fatalf("folder-scoped grant exposed %v, want none", seen)
	}
}

// An explicit deny on the library scope removes it from the listing even when a
// broader grant would otherwise allow it.
func TestListLibraries_DenyHidesLibraryFromMember(t *testing.T) {
	admin, libs, memberFn := newLibraryListServer(t)
	member := memberFn()
	vid := userIDByName(t, admin, "member")

	grantCap(t, admin, libs[0].ID, vid, authz.LibraryKey(libs[0].ID), []string{"read"})
	denyCap(t, admin, libs[0].ID, vid, authz.LibraryKey(libs[0].ID), []string{"read"})

	if seen := idsOf(listLibraries(t, member)); len(seen) != 0 {
		t.Fatalf("denied member saw %v, want none", seen)
	}
}

// Revoking the grant removes the library from the listing on the next request
// (the decision cache must be invalidated by the revoke).
func TestListLibraries_RevokedGrantRemovesLibrary(t *testing.T) {
	admin, libs, memberFn := newLibraryListServer(t)
	member := memberFn()

	rec := admin.do(t, http.MethodPost, "/api/v1/libraries/"+libs[0].ID+"/permissions",
		map[string]any{
			"user_id": userIDByName(t, admin, "member"),
			"key":     authz.LibraryKey(libs[0].ID),
			"caps":    []string{"read"},
			"effect":  "allow",
		})
	if rec.Code != http.StatusCreated {
		t.Fatalf("grant = %d, body=%s", rec.Code, rec.Body.String())
	}
	var created struct {
		Grant struct {
			ID string `json:"id"`
		} `json:"grant"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal grant: %v", err)
	}
	if created.Grant.ID == "" {
		t.Fatalf("grant response has no id: %s", rec.Body.String())
	}

	if seen := idsOf(listLibraries(t, member)); len(seen) != 1 {
		t.Fatalf("before revoke member saw %v, want one library", seen)
	}

	rec = admin.do(t, http.MethodDelete,
		"/api/v1/libraries/"+libs[0].ID+"/permissions/"+created.Grant.ID, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("revoke = %d, want 204, body=%s", rec.Code, rec.Body.String())
	}

	if seen := idsOf(listLibraries(t, member)); len(seen) != 0 {
		t.Fatalf("after revoke member saw %v, want none", seen)
	}
}

// Listing still requires a session; the endpoint is not public.
func TestListLibraries_RequiresSession(t *testing.T) {
	admin, _, _ := newLibraryListServer(t)
	anon := &testClient{handler: admin.handler}

	rec := anon.do(t, http.MethodGet, "/api/v1/libraries", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous list libraries = %d, want 401", rec.Code)
	}
}
