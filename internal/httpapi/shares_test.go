package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// createShare makes a share link and returns its token; it fails the test
// unless the server answers want.
func createShare(t *testing.T, c *testClient, libID string, body map[string]any, want int) string {
	t.Helper()
	rec := c.do(t, http.MethodPost, "/api/v1/libraries/"+libID+"/shares", body)
	if rec.Code != want {
		t.Fatalf("create share = %d, want %d (body=%s)", rec.Code, want, rec.Body.String())
	}
	var resp struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	return resp.Token
}

// public sends a request with no session, as a share visitor would.
func public(t *testing.T, c *testClient, path string) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	c.handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
	return rr
}

type publicListing struct {
	Files []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"files"`
	Folders []struct {
		RelPath string `json:"rel_path"`
	} `json:"folders"`
	Total int `json:"total"`
}

func listShare(t *testing.T, c *testClient, token, query string) (int, publicListing) {
	t.Helper()
	rr := public(t, c, "/api/v1/shares/"+token+"/files"+query)
	var out publicListing
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	return rr.Code, out
}

func names(l publicListing) []string {
	out := make([]string, 0, len(l.Files))
	for _, f := range l.Files {
		out = append(out, f.Name)
	}
	return out
}

func TestShare_RefusesALinkWithNothingChosen(t *testing.T) {
	_, admin, _, libID := newAuthzTestServer(t)

	// No target at all: once this silently shared the whole library.
	createShare(t, admin, libID, map[string]any{"caps": []string{"read"}}, http.StatusBadRequest)
	// The old raw key is refused, not guessed at.
	createShare(t, admin, libID,
		map[string]any{"key": "", "caps": []string{"read"}}, http.StatusBadRequest)
	createShare(t, admin, libID,
		map[string]any{"key": "library:" + libID, "caps": []string{"read"}}, http.StatusBadRequest)
	createShare(t, admin, libID,
		map[string]any{"target": map[string]any{}, "caps": []string{"read"}}, http.StatusBadRequest)
	createShare(t, admin, libID,
		map[string]any{"target": map[string]any{"kind": "folder"}, "caps": []string{"read"}},
		http.StatusBadRequest)
	createShare(t, admin, libID,
		map[string]any{"target": map[string]any{"kind": "planet"}, "caps": []string{"read"}},
		http.StatusBadRequest)

	rec := admin.do(t, http.MethodGet, "/api/v1/libraries/"+libID+"/shares", nil)
	var list struct {
		Shares []any `json:"shares"`
	}
	jsonUnmarshalRec(t, rec, &list)
	if len(list.Shares) != 0 {
		t.Fatalf("refused requests left %d shares behind", len(list.Shares))
	}
}

func TestShare_OnlyViewAndDownload(t *testing.T) {
	_, admin, _, libID := newAuthzTestServer(t)
	lib := map[string]any{"kind": "library"}

	createShare(t, admin, libID, map[string]any{"target": lib, "caps": []string{"read", "edit"}},
		http.StatusBadRequest)
	createShare(t, admin, libID, map[string]any{"target": lib, "caps": []string{"download"}},
		http.StatusBadRequest)
	createShare(t, admin, libID, map[string]any{"target": lib, "caps": []string{}},
		http.StatusBadRequest)
	createShare(t, admin, libID, map[string]any{"target": lib, "caps": []string{"read", "download"}},
		http.StatusCreated)
}

func TestShare_MissingTargetsAreNotFound(t *testing.T) {
	_, admin, _, libID := newAuthzTestServer(t)
	read := []string{"read"}
	for _, target := range []map[string]any{
		{"kind": "folder", "path": "no-such-folder"},
		{"kind": "folder", "path": ".orchard"}, // Orchard's own data
		{"kind": "file", "id": "nope"},
		{"kind": "tag", "id": "nope"},
		{"kind": "collection", "id": "nope"},
	} {
		rec := admin.do(t, http.MethodPost, "/api/v1/libraries/"+libID+"/shares",
			map[string]any{"target": target, "caps": read})
		if rec.Code != http.StatusNotFound && rec.Code != http.StatusBadRequest {
			t.Errorf("share %v = %d, want 404/400 (body=%s)", target, rec.Code, rec.Body.String())
		}
	}
	createShare(t, admin, libID,
		map[string]any{"target": map[string]any{"kind": "folder", "path": "../etc"}, "caps": read},
		http.StatusBadRequest)
}

func TestShare_FolderLinkOpensAtItsFolderAndNoHigher(t *testing.T) {
	_, admin, _, libID := newAuthzTestServer(t)
	inside := uploadFile(t, admin, libID, "trips/japan/temple.jpg")
	uploadFile(t, admin, libID, "trips/japan/kyoto/street.jpg")
	outside := uploadFile(t, admin, libID, "trips/paris/tower.jpg")
	uploadFile(t, admin, libID, "secret.jpg")

	token := createShare(t, admin, libID, map[string]any{
		"target": map[string]any{"kind": "folder", "path": "trips/japan"},
		"caps":   []string{"read", "download"},
	}, http.StatusCreated)

	// The link as handed out has no ?folder=: it opens at the shared folder.
	code, l := listShare(t, admin, token, "")
	if code != http.StatusOK {
		t.Fatalf("open folder share = %d, want 200", code)
	}
	if got := names(l); len(got) != 1 || got[0] != "temple.jpg" {
		t.Errorf("shared folder lists %v, want [temple.jpg]", got)
	}
	if len(l.Folders) != 1 || l.Folders[0].RelPath != "trips/japan/kyoto" {
		t.Errorf("sub-folders = %+v, want trips/japan/kyoto", l.Folders)
	}

	// Down into a sub-folder: yes. Up, sideways, or the root: no.
	if code, l := listShare(t, admin, token, "?folder=trips/japan/kyoto"); code != http.StatusOK ||
		len(l.Files) != 1 {
		t.Errorf("sub-folder = %d %v, want 200 with one file", code, names(l))
	}
	for _, q := range []string{"?folder=trips", "?folder=trips/paris", "?folder=.", "?folder=trips/japan/../paris"} {
		if code, _ := listShare(t, admin, token, q); code == http.StatusOK {
			t.Errorf("list %s = 200, want refused", q)
		}
	}

	if rr := public(t, admin, "/api/v1/shares/"+token+"/files/"+inside+"/download"); rr.Code != http.StatusOK {
		t.Errorf("download inside = %d, want 200", rr.Code)
	}
	for _, id := range []string{outside} {
		if rr := public(t, admin, "/api/v1/shares/"+token+"/files/"+id); rr.Code != http.StatusForbidden {
			t.Errorf("get outside file = %d, want 403", rr.Code)
		}
		if rr := public(t, admin, "/api/v1/shares/"+token+"/files/"+id+"/download"); rr.Code != http.StatusForbidden {
			t.Errorf("download outside file = %d, want 403", rr.Code)
		}
	}
}

func TestShare_FileLinkShowsOnlyThatFile(t *testing.T) {
	_, admin, _, libID := newAuthzTestServer(t)
	book := uploadFile(t, admin, libID, "books/dune.epub")
	other := uploadFile(t, admin, libID, "books/emma.epub")

	token := createShare(t, admin, libID, map[string]any{
		"target": map[string]any{"kind": "file", "id": book},
		"caps":   []string{"read", "download"},
	}, http.StatusCreated)

	code, l := listShare(t, admin, token, "")
	if code != http.StatusOK || len(l.Files) != 1 || l.Files[0].ID != book {
		t.Fatalf("file share lists %d %v, want just dune.epub", code, names(l))
	}
	// Asking for its folder does not widen it.
	if code, l := listShare(t, admin, token, "?folder=books"); code != http.StatusOK || len(l.Files) != 1 {
		t.Errorf("file share with ?folder = %d %v, want just the file", code, names(l))
	}
	if rr := public(t, admin, "/api/v1/shares/"+token+"/files/"+book+"/download"); rr.Code != http.StatusOK {
		t.Errorf("download shared book = %d, want 200", rr.Code)
	}
	if rr := public(t, admin, "/api/v1/shares/"+token+"/files/"+other+"/download"); rr.Code != http.StatusForbidden {
		t.Errorf("download neighbour = %d, want 403", rr.Code)
	}
}

func TestShare_TagLinkFollowsTheTag(t *testing.T) {
	_, admin, _, libID := newAuthzTestServer(t)
	tagged := uploadFile(t, admin, libID, "a/beach.jpg")
	untagged := uploadFile(t, admin, libID, "a/office.jpg")

	rec := admin.do(t, http.MethodPost, "/api/v1/libraries/"+libID+"/tags", map[string]any{"name": "summer"})
	var tag struct {
		Tag struct {
			ID string `json:"id"`
		} `json:"tag"`
	}
	jsonUnmarshalRec(t, rec, &tag)
	admin.do(t, http.MethodPost, "/api/v1/libraries/"+libID+"/files/"+tagged+"/tags",
		map[string]any{"tag_id": tag.Tag.ID})

	token := createShare(t, admin, libID, map[string]any{
		"target": map[string]any{"kind": "tag", "id": tag.Tag.ID},
		"caps":   []string{"read", "download"},
	}, http.StatusCreated)

	code, l := listShare(t, admin, token, "")
	if code != http.StatusOK || len(l.Files) != 1 || l.Files[0].ID != tagged {
		t.Fatalf("tag share lists %d %v, want just beach.jpg", code, names(l))
	}
	if rr := public(t, admin, "/api/v1/shares/"+token+"/files/"+untagged); rr.Code != http.StatusForbidden {
		t.Errorf("untagged file = %d, want 403", rr.Code)
	}

	// Taking the tag off takes the file out of the link.
	admin.roundTrip(t, http.MethodDelete,
		"/api/v1/libraries/"+libID+"/files/"+tagged+"/tags/"+tag.Tag.ID, "")
	if rr := public(t, admin, "/api/v1/shares/"+token+"/files/"+tagged+"/download"); rr.Code != http.StatusForbidden {
		t.Errorf("download after untagging = %d, want 403", rr.Code)
	}
}

func TestShare_CollectionLinkIsTheOwnersAndFollowsIt(t *testing.T) {
	_, admin, _, libID := newAuthzTestServer(t)
	in := uploadFile(t, admin, libID, "b/one.pdf")
	out := uploadFile(t, admin, libID, "b/two.pdf")

	rec := admin.do(t, http.MethodPost, "/api/v1/libraries/"+libID+"/collections",
		map[string]any{"name": "Reading"})
	var coll struct {
		Collection struct {
			ID string `json:"id"`
		} `json:"collection"`
	}
	jsonUnmarshalRec(t, rec, &coll)
	admin.do(t, http.MethodPost, "/api/v1/libraries/"+libID+"/collections/"+coll.Collection.ID+"/files",
		map[string]any{"file_id": in})

	token := createShare(t, admin, libID, map[string]any{
		"target": map[string]any{"kind": "collection", "id": coll.Collection.ID},
		"caps":   []string{"read"},
	}, http.StatusCreated)

	code, l := listShare(t, admin, token, "")
	if code != http.StatusOK || len(l.Files) != 1 || l.Files[0].ID != in {
		t.Fatalf("collection share lists %d %v, want just one.pdf", code, names(l))
	}
	if rr := public(t, admin, "/api/v1/shares/"+token+"/files/"+out); rr.Code != http.StatusForbidden {
		t.Errorf("file outside the collection = %d, want 403", rr.Code)
	}
	// Read-only: no download even for a member.
	if rr := public(t, admin, "/api/v1/shares/"+token+"/files/"+in+"/download"); rr.Code != http.StatusForbidden {
		t.Errorf("download on a view-only link = %d, want 403", rr.Code)
	}

	admin.roundTrip(t, http.MethodDelete,
		"/api/v1/libraries/"+libID+"/collections/"+coll.Collection.ID+"/files/"+in, "")
	if code, l := listShare(t, admin, token, ""); code != http.StatusOK || len(l.Files) != 0 {
		t.Errorf("after removing it, collection share lists %d %v, want empty", code, names(l))
	}
}

func TestShare_InfoNamesTheTargetWithoutInternalKeys(t *testing.T) {
	_, admin, _, libID := newAuthzTestServer(t)
	uploadFile(t, admin, libID, "trips/japan/temple.jpg")
	token := createShare(t, admin, libID, map[string]any{
		"target": map[string]any{"kind": "folder", "path": "trips/japan"},
		"caps":   []string{"read"},
	}, http.StatusCreated)

	rr := public(t, admin, "/api/v1/shares/"+token)
	if rr.Code != http.StatusOK {
		t.Fatalf("share info = %d", rr.Code)
	}
	body := rr.Body.String()
	var info struct {
		Share struct {
			Kind        string `json:"kind"`
			Name        string `json:"name"`
			Folder      string `json:"folder"`
			CanDownload bool   `json:"can_download"`
		} `json:"share"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &info)
	if info.Share.Kind != "folder" || info.Share.Name != "japan" || info.Share.Folder != "trips/japan" ||
		info.Share.CanDownload {
		t.Errorf("share info = %+v", info.Share)
	}
	if strings.Contains(body, libID) || strings.Contains(body, "f:") {
		t.Errorf("public info leaks an internal key: %s", body)
	}

	// The owner's list says what each link is, in words.
	rec := admin.do(t, http.MethodGet, "/api/v1/libraries/"+libID+"/shares", nil)
	var list struct {
		Shares []struct {
			TargetKind string `json:"target_kind"`
			TargetName string `json:"target_name"`
		} `json:"shares"`
	}
	jsonUnmarshalRec(t, rec, &list)
	if len(list.Shares) != 1 || list.Shares[0].TargetKind != "folder" || list.Shares[0].TargetName != "japan" {
		t.Errorf("owner's share list = %+v", list.Shares)
	}
}

func TestShare_DownloadNameIsQuotedSafely(t *testing.T) {
	_, admin, _, libID := newAuthzTestServer(t)
	id := uploadFile(t, admin, libID, `odd "name".txt`)
	token := createShare(t, admin, libID, map[string]any{
		"target": map[string]any{"kind": "file", "id": id},
		"caps":   []string{"read", "download"},
	}, http.StatusCreated)

	rr := public(t, admin, "/api/v1/shares/"+token+"/files/"+id+"/download")
	if rr.Code != http.StatusOK {
		t.Fatalf("download = %d", rr.Code)
	}
	got := rr.Header().Get("Content-Disposition")
	if !strings.HasPrefix(got, "attachment;") || strings.Count(got, `"`)%2 != 0 ||
		!strings.Contains(got, `\"name\"`) {
		t.Errorf("Content-Disposition = %q, want the quotes escaped", got)
	}
}

func TestLibraries_SayWhetherTheCallerMayShare(t *testing.T) {
	_, admin, viewer, libID := newAuthzTestServer(t)
	canShare := func(c *testClient) bool {
		rec := c.do(t, http.MethodGet, "/api/v1/libraries", nil)
		var resp struct {
			Libraries []struct {
				ID       string `json:"id"`
				CanShare bool   `json:"can_share"`
			} `json:"libraries"`
		}
		jsonUnmarshalRec(t, rec, &resp)
		for _, l := range resp.Libraries {
			if l.ID == libID {
				return l.CanShare
			}
		}
		return false
	}
	if !canShare(admin) {
		t.Error("admin can_share = false, want true")
	}
	if canShare(viewer) {
		t.Error("read-only viewer can_share = true, want false")
	}
}

func TestShare_RejectsAnExpiryInThePast(t *testing.T) {
	_, admin, _, libID := newAuthzTestServer(t)
	lib := map[string]any{"kind": "library"}
	createShare(t, admin, libID, map[string]any{
		"target": lib, "caps": []string{"read"}, "expires_at": "2001-01-01T00:00:00Z",
	}, http.StatusBadRequest)
	createShare(t, admin, libID, map[string]any{
		"target": lib, "caps": []string{"read"}, "expires_at": "tomorrow",
	}, http.StatusBadRequest)
}

func TestShare_WrongPasswordsLockTheLinkOnEveryRoute(t *testing.T) {
	_, admin, _, libID := newAuthzTestServer(t)
	uploadFile(t, admin, libID, "a/x.jpg")
	token := createShare(t, admin, libID, map[string]any{
		"target": map[string]any{"kind": "library"}, "caps": []string{"read"}, "password": "right-password",
	}, http.StatusCreated)

	guess := func(password string) int {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/shares/"+token+"/files", nil)
		req.Header.Set(sharePasswordHeader, password)
		rr := httptest.NewRecorder()
		admin.handler.ServeHTTP(rr, req)
		return rr.Code
	}
	// Browsing with the right password is never throttled.
	for range 30 {
		if code := guess("right-password"); code != http.StatusOK {
			t.Fatalf("right password = %d, want 200", code)
		}
	}
	// Guessing on the listing route (not /authenticate) still locks out.
	for range rateLimitFailureThreshold {
		guess("wrong")
	}
	if code := guess("right-password"); code != http.StatusTooManyRequests {
		t.Errorf("after %d wrong guesses = %d, want 429", rateLimitFailureThreshold, code)
	}
}

func TestGrant_ScopeGivesRealAccessAndFakeKeysAreRefused(t *testing.T) {
	_, admin, viewer, libID := newAuthzTestServer(t)
	vid := viewerID(t, admin)
	uploadFile(t, admin, libID, "family/beach.jpg")
	uploadFile(t, admin, libID, "work/report.pdf")

	// What the web UI used to send: stored as a folder named "library:<id>".
	rec := admin.do(t, http.MethodPost, "/api/v1/libraries/"+libID+"/permissions", map[string]any{
		"user_id": vid, "key": "library:" + libID, "caps": []string{"read"}, "effect": "allow",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("library:<id> key = %d, want 400", rec.Code)
	}

	rec = admin.do(t, http.MethodPost, "/api/v1/libraries/"+libID+"/permissions", map[string]any{
		"user_id": vid, "scope": map[string]any{"kind": "folder", "path": "/family/"},
		"caps": []string{"read"}, "effect": "allow",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("folder grant = %d, body=%s", rec.Code, rec.Body.String())
	}
	var created struct {
		Grant struct {
			ScopeKind string `json:"scope_kind"`
			ScopePath string `json:"scope_path"`
		} `json:"grant"`
	}
	jsonUnmarshalRec(t, rec, &created)
	if created.Grant.ScopeKind != "folder" || created.Grant.ScopePath != "family" {
		t.Errorf("grant scope = %+v, want folder family", created.Grant)
	}

	if rec := viewer.do(t, http.MethodGet, "/api/v1/libraries/"+libID+"/files?folder=family", nil); rec.Code != http.StatusOK {
		t.Errorf("viewer lists granted folder = %d, want 200", rec.Code)
	}
	if rec := viewer.do(t, http.MethodGet, "/api/v1/libraries/"+libID+"/files?folder=work", nil); rec.Code != http.StatusForbidden {
		t.Errorf("viewer lists other folder = %d, want 403", rec.Code)
	}

	for _, scope := range []map[string]any{{"kind": "folder"}, {"kind": "file", "path": "../x"}, {"kind": "moon"}} {
		rec := admin.do(t, http.MethodPost, "/api/v1/libraries/"+libID+"/permissions", map[string]any{
			"user_id": vid, "scope": scope, "caps": []string{"read"}, "effect": "allow",
		})
		if rec.Code != http.StatusBadRequest {
			t.Errorf("scope %v = %d, want 400", scope, rec.Code)
		}
	}
}

func TestShare_ViewOnlyLinkCanBeLookedAtButNotDownloaded(t *testing.T) {
	_, admin, _, libID := newAuthzTestServer(t)
	in := uploadFile(t, admin, libID, "pics/a.jpg")
	out := uploadFile(t, admin, libID, "private/b.jpg")
	token := createShare(t, admin, libID, map[string]any{
		"target": map[string]any{"kind": "folder", "path": "pics"}, "caps": []string{"read"},
	}, http.StatusCreated)

	rr := public(t, admin, "/api/v1/shares/"+token+"/files/"+in+"/content")
	if rr.Code != http.StatusOK {
		t.Fatalf("view on a view-only link = %d, want 200", rr.Code)
	}
	if got := rr.Header().Get("Content-Disposition"); !strings.HasPrefix(got, "attachment") {
		t.Errorf("Content-Disposition = %q: a shared file must never render as a page", got)
	}
	if rr.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("view response is missing nosniff")
	}
	if rr := public(t, admin, "/api/v1/shares/"+token+"/files/"+in+"/download"); rr.Code != http.StatusForbidden {
		t.Errorf("download on a view-only link = %d, want 403", rr.Code)
	}
	if rr := public(t, admin, "/api/v1/shares/"+token+"/files/"+out+"/content"); rr.Code != http.StatusForbidden {
		t.Errorf("view outside the share = %d, want 403", rr.Code)
	}
}

func TestShare_PasswordUnlockCookieIsPerLinkAndDiesWithIt(t *testing.T) {
	_, admin, _, libID := newAuthzTestServer(t)
	id := uploadFile(t, admin, libID, "pics/a.jpg")
	lib := map[string]any{"kind": "library"}
	token := createShare(t, admin, libID, map[string]any{
		"target": lib, "caps": []string{"read"}, "password": "right-password",
	}, http.StatusCreated)
	other := createShare(t, admin, libID, map[string]any{
		"target": lib, "caps": []string{"read"}, "password": "other-password",
	}, http.StatusCreated)

	get := func(tok, path string, header string, cookie *http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/shares/"+tok+path, nil)
		if header != "" {
			req.Header.Set(sharePasswordHeader, header)
		}
		if cookie != nil {
			req.AddCookie(cookie)
		}
		rr := httptest.NewRecorder()
		admin.handler.ServeHTTP(rr, req)
		return rr
	}
	content := "/files/" + id + "/content"

	if rr := get(token, content, "", nil); rr.Code != http.StatusUnauthorized {
		t.Fatalf("view with nothing = %d, want 401", rr.Code)
	}
	rr := get(token, "/files", "right-password", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list with password = %d", rr.Code)
	}
	var unlock *http.Cookie
	for _, c := range rr.Result().Cookies() {
		if c.Name == shareSessionCookie {
			unlock = c
		}
	}
	if unlock == nil || !unlock.HttpOnly || unlock.Path != "/api/v1/shares/"+token {
		t.Fatalf("unlock cookie = %+v, want HttpOnly and scoped to the link", unlock)
	}
	if strings.Contains(unlock.Value, "right-password") {
		t.Fatal("the cookie carries the password")
	}

	// An <img> or a reader sends the cookie, not the header.
	if rr := get(token, content, "", unlock); rr.Code != http.StatusOK {
		t.Errorf("view with the unlock cookie = %d, want 200", rr.Code)
	}
	// The cookie unlocks its own link only.
	if rr := get(other, content, "", unlock); rr.Code != http.StatusUnauthorized {
		t.Errorf("another link with this cookie = %d, want 401", rr.Code)
	}
	// A forged cookie is nothing.
	if rr := get(token, content, "", &http.Cookie{Name: shareSessionCookie, Value: "forged"}); rr.Code != http.StatusUnauthorized {
		t.Errorf("forged cookie = %d, want 401", rr.Code)
	}

	// Revoking the link ends the unlock at once.
	rec := admin.do(t, http.MethodGet, "/api/v1/libraries/"+libID+"/shares", nil)
	var list struct {
		Shares []struct {
			ID         string `json:"id"`
			TargetName string `json:"target_name"`
		} `json:"shares"`
	}
	jsonUnmarshalRec(t, rec, &list)
	for _, sh := range list.Shares {
		admin.roundTrip(t, http.MethodDelete, "/api/v1/libraries/"+libID+"/shares/"+sh.ID, "")
	}
	if rr := get(token, content, "", unlock); rr.Code != http.StatusUnauthorized {
		t.Errorf("view after revoking = %d, want 401", rr.Code)
	}
}

func TestShareSessions_ExpireAndStayBounded(t *testing.T) {
	ss := newShareSessions()
	now := time.Now()
	ss.now = func() time.Time { return now }
	id, err := ss.issue("sh1")
	if err != nil {
		t.Fatal(err)
	}
	if !ss.unlocked(id, "sh1") || ss.unlocked(id, "sh2") {
		t.Fatal("unlock should hold for its own share only")
	}
	now = now.Add(shareSessionTTL + time.Second)
	if ss.unlocked(id, "sh1") {
		t.Error("unlock outlived its TTL")
	}
	for i := 0; i < shareSessionMax+50; i++ {
		if _, err := ss.issue("sh"); err != nil {
			t.Fatal(err)
		}
	}
	if len(ss.byID) > shareSessionMax {
		t.Errorf("sessions grew to %d, cap is %d", len(ss.byID), shareSessionMax)
	}
}
