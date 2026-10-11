package httpapi

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/Jishnu-Prasad888/Orchard/internal/librarydb"
)

// rawPut sends a raw-body PUT with the given Content-Type, for endpoints
// (like cover upload) that take an image body rather than JSON.
func rawPut(t *testing.T, c *testClient, path, contentType string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	for name, value := range c.cookies {
		req.AddCookie(&http.Cookie{Name: name, Value: value})
	}
	rec := httptest.NewRecorder()
	c.handler.ServeHTTP(rec, req)
	c.applySetCookies(rec.Result().Cookies())
	return rec
}

// setContentHash updates a seeded file's content_hash directly, since
// seedLibraryFile always inserts NULL (the indexer's scanner is what
// normally computes it).
func setContentHash(t *testing.T, libRoot, fileID, hash string) {
	t.Helper()
	db, err := librarydb.Open(filepath.Join(libRoot, ".orchard"))
	if err != nil {
		t.Fatalf("open library db: %v", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`UPDATE indexed_files SET content_hash = ? WHERE id = ?`, hash, fileID); err != nil {
		t.Fatalf("set content hash: %v", err)
	}
}

func TestBookMetadata_GetDefaultsEmpty(t *testing.T) {
	_, client, libID, libRoot := newSearchTestServer(t)
	if err := seedLibraryFile(t, libRoot, "f1", "book.epub", 10); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	rec := client.do(t, http.MethodGet, "/api/v1/libraries/"+libID+"/files/f1/book-metadata", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Book struct {
			FileID     string `json:"file_id"`
			Title      string `json:"title"`
			UserEdited bool   `json:"user_edited"`
		} `json:"book"`
	}
	jsonUnmarshalRec(t, rec, &body)
	if body.Book.FileID != "f1" || body.Book.Title != "" || body.Book.UserEdited {
		t.Errorf("book = %+v", body.Book)
	}
}

func TestBookMetadata_UpdateAndGetRoundTrip(t *testing.T) {
	_, client, libID, libRoot := newSearchTestServer(t)
	if err := seedLibraryFile(t, libRoot, "f1", "book.epub", 10); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	rec := client.do(t, http.MethodPatch, "/api/v1/libraries/"+libID+"/files/f1/book-metadata",
		map[string]any{"title": "The Great Test", "author": "Jane Author"})
	if rec.Code != http.StatusOK {
		t.Fatalf("patch status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Book struct {
			Title      string `json:"title"`
			Author     string `json:"author"`
			UserEdited bool   `json:"user_edited"`
		} `json:"book"`
	}
	jsonUnmarshalRec(t, rec, &body)
	if body.Book.Title != "The Great Test" || body.Book.Author != "Jane Author" || !body.Book.UserEdited {
		t.Fatalf("book after patch = %+v", body.Book)
	}

	// Patching only one field leaves the other alone.
	rec = client.do(t, http.MethodPatch, "/api/v1/libraries/"+libID+"/files/f1/book-metadata",
		map[string]any{"title": "Corrected Title"})
	jsonUnmarshalRec(t, rec, &body)
	if body.Book.Title != "Corrected Title" || body.Book.Author != "Jane Author" {
		t.Errorf("book after second patch = %+v", body.Book)
	}

	rec = client.do(t, http.MethodGet, "/api/v1/libraries/"+libID+"/files/f1/book-metadata", nil)
	jsonUnmarshalRec(t, rec, &body)
	if body.Book.Title != "Corrected Title" {
		t.Errorf("GET after patch = %+v", body.Book)
	}
}

func TestBookMetadata_UnknownFile(t *testing.T) {
	_, client, libID, _ := newSearchTestServer(t)
	rec := client.do(t, http.MethodGet, "/api/v1/libraries/"+libID+"/files/ghost/book-metadata", nil)
	assertErrorEnvelope(t, rec, http.StatusNotFound, CodeNotFound)
}

func TestCover_UploadServeAndReject(t *testing.T) {
	_, client, libID, libRoot := newSearchTestServer(t)
	if err := seedLibraryFile(t, libRoot, "f1", "book.epub", 10); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	// No cover yet.
	rec := client.roundTrip(t, http.MethodGet, "/api/v1/libraries/"+libID+"/files/f1/cover", "")
	assertErrorEnvelope(t, rec, http.StatusNotFound, CodeNotFound)

	// Upload one.
	rec = rawPut(t, client, "/api/v1/libraries/"+libID+"/files/f1/cover",
		"image/jpeg", []byte("\xff\xd8\xff\xe0fake-jpeg-bytes"))
	if rec.Code != http.StatusOK {
		t.Fatalf("upload cover status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Book struct {
			CoverURL   string `json:"cover_url"`
			UserEdited bool   `json:"user_edited"`
		} `json:"book"`
	}
	jsonUnmarshalRec(t, rec, &body)
	if body.Book.CoverURL == "" || !body.Book.UserEdited {
		t.Fatalf("book after cover upload = %+v", body.Book)
	}

	// Now it serves.
	rec = client.roundTrip(t, http.MethodGet, "/api/v1/libraries/"+libID+"/files/f1/cover", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get cover status = %d", rec.Code)
	}
	if rec.Body.String() != "\xff\xd8\xff\xe0fake-jpeg-bytes" {
		t.Errorf("cover bytes did not round-trip, got %q", rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("Content-Type = %q, want image/jpeg", ct)
	}

	// An unsupported content type is rejected.
	rec = rawPut(t, client, "/api/v1/libraries/"+libID+"/files/f1/cover",
		"text/plain", []byte("not an image"))
	assertErrorEnvelope(t, rec, http.StatusBadRequest, CodeBadRequest)
}

func TestReadingStatus_RoundTripAndClear(t *testing.T) {
	_, client, libID, libRoot := newSearchTestServer(t)
	if err := seedLibraryFile(t, libRoot, "f1", "book.epub", 10); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	rec := client.do(t, http.MethodGet, "/api/v1/libraries/"+libID+"/files/f1/reading-status", nil)
	var body struct {
		Status *string `json:"status"`
	}
	jsonUnmarshalRec(t, rec, &body)
	if body.Status != nil {
		t.Fatalf("expected no status initially, got %+v", body.Status)
	}

	rec = client.do(t, http.MethodPut, "/api/v1/libraries/"+libID+"/files/f1/reading-status",
		map[string]any{"status": "reading"})
	if rec.Code != http.StatusOK {
		t.Fatalf("set status = %d, body=%s", rec.Code, rec.Body.String())
	}

	rec = client.do(t, http.MethodGet, "/api/v1/libraries/"+libID+"/files/f1/reading-status", nil)
	jsonUnmarshalRec(t, rec, &body)
	if body.Status == nil || *body.Status != "reading" {
		t.Fatalf("status = %+v, want reading", body.Status)
	}

	rec = client.roundTrip(t, http.MethodDelete, "/api/v1/libraries/"+libID+"/files/f1/reading-status", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("clear status = %d", rec.Code)
	}
	rec = client.do(t, http.MethodGet, "/api/v1/libraries/"+libID+"/files/f1/reading-status", nil)
	jsonUnmarshalRec(t, rec, &body)
	if body.Status != nil {
		t.Errorf("expected no status after clear, got %+v", body.Status)
	}
}

func TestReadingStatus_RejectsUnknownValue(t *testing.T) {
	_, client, libID, libRoot := newSearchTestServer(t)
	if err := seedLibraryFile(t, libRoot, "f1", "book.epub", 10); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	rec := client.do(t, http.MethodPut, "/api/v1/libraries/"+libID+"/files/f1/reading-status",
		map[string]any{"status": "bogus"})
	assertErrorEnvelope(t, rec, http.StatusBadRequest, CodeBadRequest)
}

func TestRecentlyOpened_MarkAndList(t *testing.T) {
	_, client, libID, libRoot := newSearchTestServer(t)
	if err := seedLibraryFile(t, libRoot, "f1", "book1.epub", 10); err != nil {
		t.Fatalf("seed f1: %v", err)
	}
	if err := seedLibraryFile(t, libRoot, "f2", "book2.epub", 10); err != nil {
		t.Fatalf("seed f2: %v", err)
	}

	rec := client.roundTrip(t, http.MethodGet, "/api/v1/libraries/"+libID+"/files/recently-opened", "")
	var body struct {
		Files []struct{ ID string } `json:"files"`
	}
	jsonUnmarshalRec(t, rec, &body)
	if len(body.Files) != 0 {
		t.Fatalf("expected no recently-opened files yet, got %+v", body.Files)
	}

	rec = client.roundTrip(t, http.MethodPost, "/api/v1/libraries/"+libID+"/files/f1/opened", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("mark opened status = %d, body=%s", rec.Code, rec.Body.String())
	}

	rec = client.roundTrip(t, http.MethodGet, "/api/v1/libraries/"+libID+"/files/recently-opened", "")
	jsonUnmarshalRec(t, rec, &body)
	if len(body.Files) != 1 || body.Files[0].ID != "f1" {
		t.Fatalf("recently-opened = %+v", body.Files)
	}
}

func TestDuplicates_ListsSharedContentHash(t *testing.T) {
	_, client, libID, libRoot := newSearchTestServer(t)
	if err := seedLibraryFile(t, libRoot, "f1", "a.epub", 10); err != nil {
		t.Fatal(err)
	}
	if err := seedLibraryFile(t, libRoot, "f2", "a-copy.epub", 10); err != nil {
		t.Fatal(err)
	}
	if err := seedLibraryFile(t, libRoot, "f3", "unique.epub", 10); err != nil {
		t.Fatal(err)
	}
	setContentHash(t, libRoot, "f1", "same-hash")
	setContentHash(t, libRoot, "f2", "same-hash")
	setContentHash(t, libRoot, "f3", "other-hash")

	rec := client.roundTrip(t, http.MethodGet, "/api/v1/libraries/"+libID+"/files/duplicates", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Groups []struct {
			ContentHash string                `json:"content_hash"`
			Files       []struct{ ID string } `json:"files"`
		} `json:"groups"`
	}
	jsonUnmarshalRec(t, rec, &body)
	if len(body.Groups) != 1 {
		t.Fatalf("groups = %+v, want 1", body.Groups)
	}
	if body.Groups[0].ContentHash != "same-hash" || len(body.Groups[0].Files) != 2 {
		t.Errorf("group = %+v", body.Groups[0])
	}
}
