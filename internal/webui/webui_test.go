package webui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildTestDist writes a minimal real-world-like build output to a temp
// directory and returns its path.
func buildTestDist(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	files := map[string]string{
		"index.html":       "<html><body id=\"app\"></body></html>",
		"assets/app.js":    "console.log('cairn');",
		"assets/app.css":   "body { color: red }",
		"favicon.svg":      "<svg/>",
		"nested/page.html": "<html><body>nested</body></html>",
	}
	for name, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func newTestSPA(t *testing.T) http.Handler {
	t.Helper()
	dir := buildTestDist(t)
	h, err := Handler(dir)
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	return h
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestServesIndexAtRoot(t *testing.T) {
	h := newTestSPA(t)
	rec := get(t, h, "/")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "app") {
		t.Errorf("body does not contain index.html: %q", rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("index Cache-Control = %q, want no-cache", got)
	}
}

func TestSPAFallbackForUnknownPaths(t *testing.T) {
	h := newTestSPA(t)

	for _, path := range []string{"/albums", "/photos/2024/vacation", "/memories/abc123"} {
		rec := get(t, h, path)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200 (SPA fallback)", path, rec.Code)
		}
		if rec.Body.String() != "<html><body id=\"app\"></body></html>" {
			t.Errorf("%s: body is not index.html", path)
		}
	}
}

func TestServesAssetsWithImmutableCache(t *testing.T) {
	h := newTestSPA(t)

	for _, path := range []string{"/assets/app.js", "/assets/app.css"} {
		rec := get(t, h, path)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200", path, rec.Code)
		}
		if got := rec.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
			t.Errorf("%s: Cache-Control = %q", path, got)
		}
	}
}

func TestServesExplicitIndex(t *testing.T) {
	h := newTestSPA(t)
	rec := get(t, h, "/index.html")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestRejectsTraversal(t *testing.T) {
	h := newTestSPA(t)

	for _, path := range []string{
		"/../etc/passwd",
		"/../../secret",
		"/..%2f..%2fsecret",
		"/a/../..",
		"/..",
		"/%2e%2e/",
	} {
		rec := get(t, h, path)
		if rec.Code == http.StatusOK && !strings.Contains(rec.Body.String(), "app") {
			// A 200 with arbitrary file content would be a leak; only the SPA
			// index is an acceptable 200 here.
			t.Errorf("%s: served unexpected content", path)
		}
	}
}

func TestMethodNotAllowed(t *testing.T) {
	h := newTestSPA(t)
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("x"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestCleanPath(t *testing.T) {
	cases := []struct {
		in  string
		out string
		err bool
	}{
		{"/", "index.html", false},
		{"", "index.html", false},
		{"/albums", "albums", false},
		{"/assets/app.js", "assets/app.js", false},
		{"/a//b", "a/b", false},
		{"/../x", "", true},
		{"/a/../../", "", true},
		{"../../../x", "", true},
	}
	for _, tc := range cases {
		cleaned, err := cleanPath(tc.in)
		if tc.err {
			if err == nil {
				t.Errorf("cleanPath(%q) expected error, got %q", tc.in, cleaned)
			}
			continue
		}
		if err != nil {
			t.Errorf("cleanPath(%q): %v", tc.in, err)
			continue
		}
		// cleanPath returns "" or "." for root-like input; the SPA maps it to
		// index.html.
		want := tc.out
		if want == "index.html" && (cleaned == "" || cleaned == ".") {
			continue
		}
		if cleaned != want {
			t.Errorf("cleanPath(%q) = %q, want %q", tc.in, cleaned, want)
		}
	}
}

func TestHandlerMissingDistDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "does-not-exist")
	if _, err := Handler(dir); err == nil {
		t.Error("expected error for missing dist directory")
	}
}

func TestHandlerDistDirWithoutIndex(t *testing.T) {
	dir := t.TempDir()
	if _, err := Handler(dir); err == nil {
		t.Error("expected error for dist directory without index.html")
	}
}

func TestEmbeddedHandler(t *testing.T) {
	h, err := Handler("")
	if err != nil {
		t.Fatalf("Handler(\"\"): %v", err)
	}
	rec := get(t, h, "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("embedded / status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Cairn") {
		t.Errorf("embedded placeholder does not reference Cairn")
	}
}
