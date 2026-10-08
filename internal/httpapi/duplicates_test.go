package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/Jishnu-Prasad888/Cairn/internal/librarydb"
)

// seedLibraryFileHash inserts a present indexed_files row with an explicit
// content hash into a library's per-library database.
func seedLibraryFileHash(t *testing.T, libRoot, id, relPath string, size int64, hash string) {
	t.Helper()
	ldb, err := librarydb.Open(filepath.Join(libRoot, ".cairn"))
	if err != nil {
		t.Fatalf("open library db: %v", err)
	}
	defer func() { _ = ldb.Close() }()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = ldb.Exec(`INSERT INTO indexed_files
		(id, rel_path, size_bytes, mod_time, content_hash, status, first_seen_at, last_seen_at, indexed_at)
		VALUES (?, ?, ?, ?, ?, 'present', ?, ?, ?)`,
		id, relPath, size, now, hash, now, now, now)
	if err != nil {
		t.Fatalf("insert %s: %v", relPath, err)
	}
}

func decodeDuplicates(t *testing.T, rec *httptest.ResponseRecorder) struct {
	Groups []struct {
		ContentHash string `json:"content_hash"`
		SizeBytes   int64  `json:"size_bytes"`
		Files       []struct {
			ID          string `json:"id"`
			RelPath     string `json:"rel_path"`
			Name        string `json:"name"`
			SizeBytes   int64  `json:"size_bytes"`
			ContentHash string `json:"content_hash"`
		} `json:"files"`
	} `json:"groups"`
	NextCursor string `json:"next_cursor"`
	Total      int    `json:"total"`
} {
	t.Helper()
	var body struct {
		Groups []struct {
			ContentHash string `json:"content_hash"`
			SizeBytes   int64  `json:"size_bytes"`
			Files       []struct {
				ID          string `json:"id"`
				RelPath     string `json:"rel_path"`
				Name        string `json:"name"`
				SizeBytes   int64  `json:"size_bytes"`
				ContentHash string `json:"content_hash"`
			} `json:"files"`
		} `json:"groups"`
		NextCursor string `json:"next_cursor"`
		Total      int    `json:"total"`
	}
	data, err := io.ReadAll(rec.Result().Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("decode body %q: %v", data, err)
	}
	return body
}

func TestHandleListDuplicates(t *testing.T) {
	_, client, libID, libRoot := newSearchTestServer(t)

	const hashA = "sha256-dup"
	seedLibraryFileHash(t, libRoot, "f1", "IMG_0001.png", 1000, hashA)
	seedLibraryFileHash(t, libRoot, "f2", "2024/IMG_0001_copy.png", 1000, hashA)
	seedLibraryFileHash(t, libRoot, "f3", "notes.txt", 200, "sha256-uniq")

	rec := client.do(t, http.MethodGet, "/api/v1/libraries/"+libID+"/files/duplicates", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	body := decodeDuplicates(t, rec)

	if body.Total != 1 {
		t.Errorf("total = %d, want 1", body.Total)
	}
	if body.NextCursor != "" {
		t.Errorf("next_cursor = %q, want empty", body.NextCursor)
	}
	if len(body.Groups) != 1 {
		t.Fatalf("groups = %d, want 1", len(body.Groups))
	}
	g := body.Groups[0]
	if g.ContentHash != hashA {
		t.Errorf("group hash = %q, want %q", g.ContentHash, hashA)
	}
	if g.SizeBytes != 1000 {
		t.Errorf("group size = %d, want 1000", g.SizeBytes)
	}
	if len(g.Files) != 2 {
		t.Fatalf("members = %d, want 2", len(g.Files))
	}
	names := map[string]bool{}
	for _, f := range g.Files {
		names[f.Name] = true
		if f.ContentHash != hashA {
			t.Errorf("member %s hash = %q, want %q", f.Name, f.ContentHash, hashA)
		}
	}
	if !names["IMG_0001.png"] || !names["IMG_0001_copy.png"] {
		t.Errorf("members = %v, want both IMG_0001 files", names)
	}
}

func TestHandleListDuplicatesPagination(t *testing.T) {
	_, client, libID, libRoot := newSearchTestServer(t)

	seedLibraryFileHash(t, libRoot, "a1", "a-1.txt", 10, "sha256-a")
	seedLibraryFileHash(t, libRoot, "a2", "a-2.txt", 10, "sha256-a")
	seedLibraryFileHash(t, libRoot, "b1", "b-1.txt", 20, "sha256-b")
	seedLibraryFileHash(t, libRoot, "b2", "b-2.txt", 20, "sha256-b")

	rec := client.do(t, http.MethodGet,
		"/api/v1/libraries/"+libID+"/files/duplicates?limit=1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("page1 status = %d", rec.Code)
	}
	page1 := decodeDuplicates(t, rec)
	if page1.Total != 2 || len(page1.Groups) != 1 || page1.NextCursor == "" {
		t.Fatalf("page1 = total=%d groups=%d cursor=%q, want 2/1/cursor",
			page1.Total, len(page1.Groups), page1.NextCursor)
	}

	rec = client.do(t, http.MethodGet,
		"/api/v1/libraries/"+libID+"/files/duplicates?limit=1&cursor="+page1.NextCursor, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("page2 status = %d", rec.Code)
	}
	page2 := decodeDuplicates(t, rec)
	if len(page2.Groups) != 1 || page2.Groups[0].ContentHash == page1.Groups[0].ContentHash {
		t.Errorf("page2 did not advance past cursor: %+v", page2.Groups)
	}
	if page2.NextCursor != "" {
		t.Errorf("page2 cursor = %q, want empty (last page)", page2.NextCursor)
	}
}

func TestHandleListDuplicatesEmpty(t *testing.T) {
	_, client, libID, libRoot := newSearchTestServer(t)
	seedLibraryFileHash(t, libRoot, "f1", "notes.txt", 5, "") // no hash

	rec := client.do(t, http.MethodGet, "/api/v1/libraries/"+libID+"/files/duplicates", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := decodeDuplicates(t, rec)
	if body.Total != 0 || len(body.Groups) != 0 || body.NextCursor != "" {
		t.Errorf("empty = total=%d groups=%d cursor=%q, want 0/0/''",
			body.Total, len(body.Groups), body.NextCursor)
	}
}

func TestHandleListDuplicatesRequiresAuth(t *testing.T) {
	handler, _, libID, _ := newSearchTestServer(t)

	rec := doJSON(t, handler, http.MethodGet, "/api/v1/libraries/"+libID+"/files/duplicates")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}
