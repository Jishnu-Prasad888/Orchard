package httpapi

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Jishnu-Prasad888/Cairn/internal/db"
)

// newCaptureServer is newTestServer with a logger that writes into the
// returned buffer, so middleware log lines can be asserted on.
func newCaptureServer(t *testing.T) (http.Handler, *bytes.Buffer) {
	t.Helper()
	pool, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	srv := New(Dependencies{Logger: logger, DB: pool})
	return srv.Handler(), &buf
}

func TestSecurityHeadersPresent(t *testing.T) {
	handler, _ := newTestServer(t)
	rec := doJSON(t, handler, http.MethodGet, "/api/v1/health")

	for _, hdr := range []string{
		"X-Content-Type-Options",
		"X-Frame-Options",
		"Referrer-Policy",
		"Content-Security-Policy",
		"Cross-Origin-Opener-Policy",
	} {
		if rec.Header().Get(hdr) == "" {
			t.Errorf("response missing security header %q", hdr)
		}
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
}

func TestSameOriginAllowsMatchingHost(t *testing.T) {
	handler, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/bootstrap", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://"+req.Host) // httptest Host is example.com → default port 80
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code == http.StatusForbidden {
		t.Fatalf("same-origin mutation returned 403; want it to reach the handler")
	}
}

func TestSameOriginRejectsCrossSite(t *testing.T) {
	handler, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/bootstrap", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin mutation: status = %d, want 403", rec.Code)
	}
	if rec.Body.Len() == 0 {
		t.Fatal("403 must carry a JSON error body")
	}
}

func TestSameOriginIgnoresSafeMethods(t *testing.T) {
	handler, _ := newTestServer(t)
	// Reads carry no CSRF risk; even a hostile Origin must not block them.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET with foreign Origin: status = %d, want 200", rec.Code)
	}
}

func TestShareTokenRedactedFromAccessLog(t *testing.T) {
	handler, buf := newCaptureServer(t)
	token := "verysecrethishtoken123"
	req := httptest.NewRequest(http.MethodGet, "/api/v1/shares/"+token+"/files", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	logged := buf.String()
	if strings.Contains(logged, token) {
		t.Fatalf("access log leaked the share token: %s", logged)
	}
	if !strings.Contains(logged, "[redacted]") {
		t.Errorf("access log did not redact the token segment: %s", logged)
	}
}

func TestRedactPath(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"/api/v1/health", "/api/v1/health"},
		{"/api/v1/shares/tok123/files", "/api/v1/shares/[redacted]/files"},
		{"/api/v1/shares/tok123", "/api/v1/shares/[redacted]"},
		{"/api/v1/shares/tok123/", "/api/v1/shares/[redacted]/"},
	}
	for _, tc := range cases {
		if got := redactPath(tc.in); got != tc.want {
			t.Errorf("redactPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
