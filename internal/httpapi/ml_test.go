package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Jishnu-Prasad888/Cairn/internal/audit"
	"github.com/Jishnu-Prasad888/Cairn/internal/auth"
	"github.com/Jishnu-Prasad888/Cairn/internal/authz"
	"github.com/Jishnu-Prasad888/Cairn/internal/crypto"
	"github.com/Jishnu-Prasad888/Cairn/internal/db"
	"github.com/Jishnu-Prasad888/Cairn/internal/indexer"
	"github.com/Jishnu-Prasad888/Cairn/internal/library"
	"github.com/Jishnu-Prasad888/Cairn/internal/librarydb"
	"github.com/Jishnu-Prasad888/Cairn/internal/ml"
)

// newMLTestServer wires a server with an ML manager, one registered library,
// and a bootstrapped admin session.
func newMLTestServer(t *testing.T) (http.Handler, *testClient, *sql.DB, string) {
	t.Helper()
	dir := t.TempDir()
	pool, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}

	logger := testLogger()
	audSvc := audit.New(pool, logger)
	authSvc := auth.NewService(pool, logger, audSvc)
	authzSvc := authz.NewService(pool, logger, audSvc)
	libraries := library.NewManager(pool, logger, audSvc, crypto.NewKeys(""))

	libRoot := filepath.Join(dir, "media")
	if err := os.MkdirAll(filepath.Join(libRoot, "holiday"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// beach.png and beach2.png share a gradient (a near-duplicate pair);
	// forest.png is a checkerboard — a different spatial pattern, not just a
	// different color, since average-hash is invariant to uniform brightness
	// scaling and a solid fill of any color collapses to the same signature.
	writeGradientPNG(t, filepath.Join(libRoot, "holiday", "beach.png"), 96, 96)
	writeGradientPNG(t, filepath.Join(libRoot, "holiday", "beach2.png"), 96, 96)
	writeCheckerPNG(t, filepath.Join(libRoot, "forest.png"), 96, 96)
	lib, err := libraries.Create(context.Background(), libRoot, "media")
	if err != nil {
		t.Fatalf("create library: %v", err)
	}
	libID := lib.ID

	// Index the files directly so the library DB has rows to sign.
	cairnDir := filepath.Join(libRoot, ".cairn")
	ldb, err := librarydb.Open(cairnDir)
	if err != nil {
		t.Fatalf("open library db: %v", err)
	}
	now := "2026-01-01T00:00:00.000Z"
	for i, rel := range []string{"holiday/beach.png", "holiday/beach2.png", "forest.png"} {
		if _, err := ldb.ExecContext(context.Background(), `
			INSERT INTO indexed_files (id, rel_path, size_bytes, mod_time, status, first_seen_at, last_seen_at, indexed_at)
			VALUES (?, ?, 1, ?, 'present', ?, ?, ?)`,
			"file"+string(rune('a'+i)), rel, now, now, now, now); err != nil {
			t.Fatalf("index file: %v", err)
		}
	}
	if err := ldb.Close(); err != nil {
		t.Fatalf("close library db: %v", err)
	}

	mlMgr := ml.NewManager(logger, ml.Config{Enabled: true, Workers: 2}, ml.AverageHashProvider{})

	handler := New(Dependencies{
		Logger:    logger,
		DB:        pool,
		Auth:      authSvc,
		Authz:     authzSvc,
		Libraries: libraries,
		Indexer:   indexer.NewIndexManager(logger),
		ML:        mlMgr,
	}).Handler()

	client := &testClient{handler: handler}
	bootstrap(t, client)

	// Admin runs an initial similarity pass so the store has signatures.
	rec := client.roundTrip(t, http.MethodPost, "/api/v1/libraries/"+libID+"/ml/similarity/pass", "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("initial similarity pass status = %d body=%s", rec.Code, rec.Body.String())
	}
	return handler, client, pool, libID
}

func writeTestPNG(t *testing.T, path string, w, h int, c color.RGBA) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			img.Set(x, y, c)
		}
	}
	encodeTestPNG(t, path, img)
}

// writeGradientPNG writes a decodable PNG with a smooth diagonal gradient, so
// its average hash carries real spatial structure rather than collapsing to
// the all-pixels-equal-the-mean signature every solid fill hashes to.
func writeGradientPNG(t *testing.T, path string, w, h int) {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			img.SetGray(x, y, color.Gray{Y: uint8(255 * (x + y) / (w + h))})
		}
	}
	encodeTestPNG(t, path, img)
}

// writeCheckerPNG writes a decodable PNG with a coarse checkerboard — a
// spatial pattern far from a smooth gradient's, standing in for "a genuinely
// different picture" in similarity tests.
func writeCheckerPNG(t *testing.T, path string, w, h int) {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, w, h))
	const cell = 12
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			v := uint8(0)
			if (x/cell+y/cell)%2 == 0 {
				v = 255
			}
			img.SetGray(x, y, color.Gray{Y: v})
		}
	}
	encodeTestPNG(t, path, img)
}

func encodeTestPNG(t *testing.T, path string, img image.Image) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

// decodeMLStatus parses the /ml status response.
func decodeMLStatus(t *testing.T, rec *httptest.ResponseRecorder) ml.Status {
	t.Helper()
	var out ml.Status
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal ml status %q: %v", rec.Body.String(), err)
	}
	return out
}

func TestMLEndpointsRequireAdmin(t *testing.T) {
	handler, _, _, libID := newMLTestServer(t)

	fresh := &testClient{handler: handler}
	for _, req := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/libraries/" + libID + "/ml"},
		{http.MethodPost, "/api/v1/libraries/" + libID + "/ml/similarity/pass"},
		{http.MethodPost, "/api/v1/libraries/" + libID + "/ml/purge"},
	} {
		rec := fresh.roundTrip(t, req.method, req.path, "")
		if rec.Code != http.StatusUnauthorized && rec.Code != http.StatusForbidden {
			t.Errorf("%s %s: status = %d, want 401/403", req.method, req.path, rec.Code)
		}
	}
}

func TestMLStatusAndPassFlow(t *testing.T) {
	_, client, _, libID := newMLTestServer(t)

	// Status shows the provider and a non-zero signature count. The pass runs
	// in the background, so poll until it lands.
	var rec *httptest.ResponseRecorder
	var st ml.Status
	deadline := time.Now().Add(10 * time.Second)
	for {
		rec = client.roundTrip(t, http.MethodGet, "/api/v1/libraries/"+libID+"/ml", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /ml status = %d body=%s", rec.Code, rec.Body.String())
		}
		st = decodeMLStatus(t, rec)
		if st.SigCount >= 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("signature pass did not complete: signatured = %d, want 3", st.SigCount)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !st.Enabled {
		t.Error("ml status: enabled = false, want true")
	}
	if st.Provider != "average_hash" || st.ProviderVersion != 1 {
		t.Errorf("ml status provider = %s v%d", st.Provider, st.ProviderVersion)
	}
	if st.SigCount != 3 {
		t.Errorf("ml status signatured = %d, want 3", st.SigCount)
	}

	// A second pass is a no-op (200 accepted, nothing left to sign).
	rec = client.roundTrip(t, http.MethodPost, "/api/v1/libraries/"+libID+"/ml/similarity/pass", "")
	if rec.Code != http.StatusAccepted {
		t.Errorf("second pass status = %d, want 202", rec.Code)
	}

	// Similar files: beach2 (a near-duplicate) is within range; forest (a
	// different picture) is not.
	rec = client.roundTrip(t, http.MethodGet, "/api/v1/libraries/"+libID+"/files/filea/similar", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET similar status = %d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Similar []struct {
			FileID     string  `json:"file_id"`
			FilePath   string  `json:"file_path"`
			Distance   int     `json:"distance"`
			Similarity float64 `json:"similarity"`
		} `json:"similar"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal similar: %v", err)
	}
	if len(body.Similar) != 1 {
		t.Fatalf("similar = %d results, want 1", len(body.Similar))
	}
	if body.Similar[0].FileID != "fileb" {
		t.Errorf("top similar = %+v, want fileb", body.Similar[0])
	}

	// Purge clears derived data.
	rec = client.roundTrip(t, http.MethodPost, "/api/v1/libraries/"+libID+"/ml/purge", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("POST purge status = %d body=%s", rec.Code, rec.Body.String())
	}
	rec = client.roundTrip(t, http.MethodGet, "/api/v1/libraries/"+libID+"/ml", "")
	st = decodeMLStatus(t, rec)
	if st.SigCount != 0 {
		t.Errorf("ml status after purge signatured = %d, want 0", st.SigCount)
	}
}

func TestSimilarityGroups(t *testing.T) {
	_, client, _, libID := newMLTestServer(t)

	// The pass runs in the background; poll the groups endpoint until the
	// near-duplicate pair (beach.png, beach2.png) lands in one group.
	var body struct {
		Groups []struct {
			Files []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"files"`
		} `json:"groups"`
		Total int `json:"total"`
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		rec := client.roundTrip(t, http.MethodGet,
			"/api/v1/libraries/"+libID+"/ml/similarity/groups", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET groups status = %d body=%s", rec.Code, rec.Body.String())
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("unmarshal groups: %v", err)
		}
		if body.Total >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no similarity group appeared: body=%+v", body)
		}
		time.Sleep(50 * time.Millisecond)
	}

	if body.Total != 1 || len(body.Groups) != 1 {
		t.Fatalf("groups = %+v, want exactly one group", body)
	}
	group := body.Groups[0]
	if len(group.Files) != 2 {
		t.Fatalf("group files = %d, want 2 (forest.png must not join)", len(group.Files))
	}
	names := map[string]bool{}
	for _, f := range group.Files {
		names[f.Name] = true
	}
	if !names["beach.png"] || !names["beach2.png"] {
		t.Errorf("group members = %+v, want beach.png and beach2.png", group.Files)
	}
	if names["forest.png"] {
		t.Error("forest.png (a different picture) should not be in the group")
	}
}

func TestSimilarityGroupsRequiresAuth(t *testing.T) {
	handler, _, _, libID := newMLTestServer(t)
	fresh := &testClient{handler: handler}
	rec := fresh.roundTrip(t, http.MethodGet,
		"/api/v1/libraries/"+libID+"/ml/similarity/groups", "")
	if rec.Code != http.StatusUnauthorized && rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 401/403", rec.Code)
	}
}

func TestMLDisabledEndpointUnavailable(t *testing.T) {
	dir := t.TempDir()
	pool, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}

	logger := testLogger()
	audSvc := audit.New(pool, logger)
	authSvc := auth.NewService(pool, logger, audSvc)
	authzSvc := authz.NewService(pool, logger, audSvc)
	libraries := library.NewManager(pool, logger, audSvc, crypto.NewKeys(""))

	libRoot := filepath.Join(dir, "media")
	if err := os.MkdirAll(libRoot, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	lib, err := libraries.Create(context.Background(), libRoot, "media")
	if err != nil {
		t.Fatalf("create library: %v", err)
	}
	libID := lib.ID

	// Seed one file so the similar-files lookup reaches the ML layer instead
	// of returning 404 for a nonexistent file.
	cairnDir := filepath.Join(libRoot, ".cairn")
	ldb, err := librarydb.Open(cairnDir)
	if err != nil {
		t.Fatalf("open library db: %v", err)
	}
	now := "2026-01-01T00:00:00.000Z"
	if _, err := ldb.ExecContext(context.Background(), `
		INSERT INTO indexed_files (id, rel_path, size_bytes, mod_time, status, first_seen_at, last_seen_at, indexed_at)
		VALUES ('filea', 'a.png', 1, ?, 'present', ?, ?, ?)`, now, now, now, now); err != nil {
		t.Fatalf("index file: %v", err)
	}
	if err := ldb.Close(); err != nil {
		t.Fatalf("close library db: %v", err)
	}

	// Disabled ML: the manager exists but refuses to work.
	mlMgr := ml.NewManager(logger, ml.Config{Enabled: false}, ml.AverageHashProvider{})

	handler := New(Dependencies{
		Logger:    logger,
		DB:        pool,
		Auth:      authSvc,
		Authz:     authzSvc,
		Libraries: libraries,
		Indexer:   indexer.NewIndexManager(logger),
		ML:        mlMgr,
	}).Handler()

	client := &testClient{handler: handler}
	bootstrap(t, client)

	// Action endpoints are SERVICE_UNAVAILABLE while ML is disabled.
	rec := client.roundTrip(t, http.MethodPost, "/api/v1/libraries/"+libID+"/ml/similarity/pass", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("pass on disabled ML status = %d, want 503", rec.Code)
	}
	rec = client.roundTrip(t, http.MethodPost, "/api/v1/libraries/"+libID+"/ml/purge", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("purge on disabled ML status = %d, want 503", rec.Code)
	}

	// Similar lookup returns SERVICE_UNAVAILABLE too.
	rec = client.roundTrip(t, http.MethodGet, "/api/v1/libraries/"+libID+"/files/filea/similar", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("similar on disabled ML status = %d, want 503", rec.Code)
	}
	rec = client.roundTrip(t, http.MethodGet, "/api/v1/libraries/"+libID+"/ml/similarity/groups", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("groups on disabled ML status = %d, want 503", rec.Code)
	}
}
