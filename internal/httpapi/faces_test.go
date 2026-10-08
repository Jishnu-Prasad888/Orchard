package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"image"
	"image/color"
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

// testFaceProvider reports one face per image and distinguishes the lighter
// test photos from the darker one so clustering yields two people.
type testFaceProvider struct{}

func (testFaceProvider) Name() string     { return "test_faces" }
func (testFaceProvider) Version() int     { return 1 }
func (testFaceProvider) Describe() string { return "test provider" }

func lumaAvg(img image.Image) float64 {
	b := img.Bounds()
	var sum float64
	var n int
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			sum += (float64(r>>8)*299 + float64(g>>8)*587 + float64(bl>>8)*114) / 1000
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}

func (testFaceProvider) Detect(img image.Image) ([]ml.FaceBox, error) {
	if lumaAvg(img) < 30 {
		return nil, nil
	}
	return []ml.FaceBox{{X: 20, Y: 20, Width: 24, Height: 24, Confidence: 0.9}}, nil
}

func (testFaceProvider) Embed(img image.Image, _ ml.FaceBox) ([]float32, error) {
	v := make([]float32, 256)
	if lumaAvg(img) > 160 {
		v[0] = 1
	} else {
		v[1] = 1
	}
	return v, nil
}

// newFacesTestServer wires a server with a face manager enabled, one
// registered library containing three indexed photos, and an admin session.
func newFacesTestServer(t *testing.T) (http.Handler, *testClient, *sql.DB, string) {
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
	// Two bright photos (same person) and one darker photo (someone else).
	writeTestPNG(t, filepath.Join(libRoot, "holiday", "a.png"), 96, 96,
		color.RGBA{R: 240, G: 230, B: 220, A: 255})
	writeTestPNG(t, filepath.Join(libRoot, "holiday", "b.png"), 96, 96,
		color.RGBA{R: 240, G: 230, B: 220, A: 255})
	writeTestPNG(t, filepath.Join(libRoot, "c.png"), 96, 96,
		color.RGBA{R: 40, G: 40, B: 40, A: 255})
	lib, err := libraries.Create(context.Background(), libRoot, "media")
	if err != nil {
		t.Fatalf("create library: %v", err)
	}
	libID := lib.ID

	cairnDir := filepath.Join(libRoot, ".cairn")
	ldb, err := librarydb.Open(cairnDir)
	if err != nil {
		t.Fatalf("open library db: %v", err)
	}
	now := "2026-01-01T00:00:00.000Z"
	for i, rel := range []string{"holiday/a.png", "holiday/b.png", "c.png"} {
		if _, err := ldb.ExecContext(context.Background(), `
			INSERT INTO indexed_files (id, rel_path, size_bytes, mod_time, status, first_seen_at, last_seen_at, indexed_at)
			VALUES (?, ?, 1, ?, 'present', ?, ?, ?)`,
			"f"+string(rune('a'+i)), rel, now, now, now, now); err != nil {
			t.Fatalf("index file: %v", err)
		}
		if _, err := ldb.ExecContext(context.Background(), `
			INSERT INTO media_metadata (file_id, media_type, updated_at) VALUES (?, 'photo', ?)`,
			"f"+string(rune('a'+i)), now); err != nil {
			t.Fatalf("media metadata: %v", err)
		}
	}
	if err := ldb.Close(); err != nil {
		t.Fatalf("close library db: %v", err)
	}

	faces := ml.NewFaceManager(logger, ml.FaceConfig{
		Enabled:   true,
		Workers:   2,
		Threshold: 0.9,
		MinSize:   20,
	}, testFaceProvider{})

	handler := New(Dependencies{
		Logger:    logger,
		DB:        pool,
		Auth:      authSvc,
		Authz:     authzSvc,
		Libraries: libraries,
		Indexer:   indexer.NewIndexManager(logger),
		Faces:     faces,
	}).Handler()

	client := &testClient{handler: handler}
	bootstrap(t, client)
	return handler, client, pool, libID
}

func TestFaceEndpointsRequireAdmin(t *testing.T) {
	handler, _, _, libID := newFacesTestServer(t)

	fresh := &testClient{handler: handler}
	for _, req := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/libraries/" + libID + "/ml/faces"},
		{http.MethodPost, "/api/v1/libraries/" + libID + "/ml/faces/pass"},
		{http.MethodPost, "/api/v1/libraries/" + libID + "/ml/faces/cluster"},
		{http.MethodPost, "/api/v1/libraries/" + libID + "/ml/faces/purge"},
	} {
		rec := fresh.roundTrip(t, req.method, req.path, "")
		if rec.Code != http.StatusUnauthorized && rec.Code != http.StatusForbidden {
			t.Errorf("%s %s: status = %d, want 401/403", req.method, req.path, rec.Code)
		}
	}
}

func decodeFaceStatus(t *testing.T, rec *httptest.ResponseRecorder) ml.FaceStatus {
	t.Helper()
	var out ml.FaceStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal face status %q: %v", rec.Body.String(), err)
	}
	return out
}

func waitForFaceStatus(t *testing.T, client *testClient, libID string, want func(ml.FaceStatus) bool) ml.FaceStatus {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		rec := client.roundTrip(t, http.MethodGet, "/api/v1/libraries/"+libID+"/ml/faces", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /ml/faces = %d body=%s", rec.Code, rec.Body.String())
		}
		st := decodeFaceStatus(t, rec)
		if want(st) {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("face status did not converge: %+v", st)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestFaceStatusPassClusterAndPurge(t *testing.T) {
	_, client, _, libID := newFacesTestServer(t)

	st := waitForFaceStatus(t, client, libID, func(s ml.FaceStatus) bool { return s.Enabled })
	if !st.Enabled || st.Provider != "test_faces" || st.ProviderVersion != 1 {
		t.Errorf("face status = %+v", st)
	}
	if st.Faces != 0 || st.People != 0 {
		t.Fatalf("initial faces/people = %d/%d, want 0/0", st.Faces, st.People)
	}

	// Detection pass (async).
	rec := client.roundTrip(t, http.MethodPost, "/api/v1/libraries/"+libID+"/ml/faces/pass", "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST faces/pass = %d body=%s", rec.Code, rec.Body.String())
	}
	// Detection is followed by grouping: the two bright photos share a person
	// and the lone face waits in the unassigned pool.
	waitForFaceStatus(t, client, libID, func(s ml.FaceStatus) bool {
		return s.Faces == 3 && !s.Running && s.People == 1 && s.Unassigned == 1
	})

	rec = client.roundTrip(t, http.MethodGet, "/api/v1/libraries/"+libID+"/faces", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /faces = %d", rec.Code)
	}
	var pool struct {
		Faces []map[string]any `json:"faces"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &pool); err != nil {
		t.Fatal(err)
	}
	if len(pool.Faces) != 1 {
		t.Fatalf("unassigned faces = %d, want 1", len(pool.Faces))
	}
	lone := pool.Faces[0]["id"].(string)

	// Regrouping changes nothing.
	rec = client.roundTrip(t, http.MethodPost, "/api/v1/libraries/"+libID+"/ml/faces/cluster", "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST faces/cluster = %d body=%s", rec.Code, rec.Body.String())
	}
	waitForFaceStatus(t, client, libID, func(s ml.FaceStatus) bool {
		return !s.Running && s.People == 1 && s.Unassigned == 1
	})

	// People list.
	rec = client.roundTrip(t, http.MethodGet, "/api/v1/libraries/"+libID+"/people", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /people = %d body=%s", rec.Code, rec.Body.String())
	}
	var list struct {
		People []ml.Person `json:"people"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.People) != 1 {
		t.Fatalf("people = %d, want 1", len(list.People))
	}
	var twoFace *ml.Person
	for i := range list.People {
		if list.People[i].FaceCount == 2 {
			twoFace = &list.People[i]
		}
	}
	if twoFace == nil || twoFace.CoverFaceID == "" {
		t.Fatal("no person with 2 faces + cover found")
	}

	// Face crop endpoint serves a JPEG.
	rec = client.roundTrip(t, http.MethodGet,
		"/api/v1/libraries/"+libID+"/faces/"+twoFace.CoverFaceID+"/image", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET face image = %d body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("face image content-type = %q", ct)
	}
	if rec.Body.Len() == 0 {
		t.Error("face image body empty")
	}

	// Person detail lists their two faces.
	rec = client.roundTrip(t, http.MethodGet, "/api/v1/libraries/"+libID+"/people/"+twoFace.ID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET person = %d", rec.Code)
	}
	var detail struct {
		Person ml.Person        `json:"person"`
		Faces  []map[string]any `json:"faces"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if len(detail.Faces) != 2 {
		t.Fatalf("person faces = %d, want 2", len(detail.Faces))
	}

	// Rename.
	rec = client.roundTrip(t, http.MethodPost,
		"/api/v1/libraries/"+libID+"/people/"+twoFace.ID+"/rename",
		`{"name":"Mom"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("rename = %d body=%s", rec.Code, rec.Body.String())
	}
	rec = client.roundTrip(t, http.MethodGet, "/api/v1/libraries/"+libID+"/people", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /people after rename = %d", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	renamed := false
	for _, p := range list.People {
		if p.Name == "Mom" {
			renamed = true
		}
	}
	if !renamed {
		t.Error("renamed person Missing from list")
	}

	// Place the lone face on a new person by hand, then merge them into Mom.
	rec = client.roundTrip(t, http.MethodPost, "/api/v1/libraries/"+libID+"/people", `{"name":"Visitor"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create person = %d body=%s", rec.Code, rec.Body.String())
	}
	var visitor struct {
		Person ml.Person `json:"person"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &visitor); err != nil {
		t.Fatal(err)
	}
	sourceID := visitor.Person.ID
	rec = client.roundTrip(t, http.MethodPost,
		"/api/v1/libraries/"+libID+"/people/"+sourceID+"/faces/"+lone, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("assign = %d body=%s", rec.Code, rec.Body.String())
	}
	rec = client.roundTrip(t, http.MethodPost,
		"/api/v1/libraries/"+libID+"/people/"+twoFace.ID+"/merge",
		`{"source_person_id":"`+sourceID+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("merge = %d body=%s", rec.Code, rec.Body.String())
	}
	waitForFaceStatus(t, client, libID, func(s ml.FaceStatus) bool { return s.People == 1 })

	// Pull one face off Mom, then assign it to a freshly created person.
	rec = client.roundTrip(t, http.MethodGet, "/api/v1/libraries/"+libID+"/people/"+twoFace.ID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET person = %d", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if len(detail.Faces) != 3 {
		t.Fatalf("person faces after merge = %d, want 3", len(detail.Faces))
	}
	orphan := detail.Faces[0]["id"].(string)

	rec = client.roundTrip(t, http.MethodDelete,
		"/api/v1/libraries/"+libID+"/people/"+twoFace.ID+"/faces/"+orphan, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("unassign = %d body=%s", rec.Code, rec.Body.String())
	}
	waitForFaceStatus(t, client, libID, func(s ml.FaceStatus) bool { return s.Unassigned == 1 })

	rec = client.roundTrip(t, http.MethodGet, "/api/v1/libraries/"+libID+"/faces", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &pool); err != nil {
		t.Fatal(err)
	}
	if len(pool.Faces) != 1 {
		t.Fatalf("unassigned faces = %d, want 1", len(pool.Faces))
	}

	rec = client.roundTrip(t, http.MethodPost, "/api/v1/libraries/"+libID+"/people", `{"name":"Guest"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create person = %d body=%s", rec.Code, rec.Body.String())
	}
	var created struct {
		Person ml.Person `json:"person"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	rec = client.roundTrip(t, http.MethodPost,
		"/api/v1/libraries/"+libID+"/people/"+created.Person.ID+"/faces/"+orphan, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("assign = %d body=%s", rec.Code, rec.Body.String())
	}
	waitForFaceStatus(t, client, libID, func(s ml.FaceStatus) bool {
		return s.People == 2 && s.Unassigned == 0
	})

	// Merge fails on itself, a missing person 404s, and delete removes Guest.
	rec = client.roundTrip(t, http.MethodPost,
		"/api/v1/libraries/"+libID+"/people/"+created.Person.ID+"/merge",
		`{"source_person_id":"`+created.Person.ID+`"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("self-merge status = %d, want 400", rec.Code)
	}
	rec = client.roundTrip(t, http.MethodDelete,
		"/api/v1/libraries/"+libID+"/people/nope", "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("delete missing person status = %d, want 404", rec.Code)
	}
	rec = client.roundTrip(t, http.MethodDelete,
		"/api/v1/libraries/"+libID+"/people/"+created.Person.ID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("delete person = %d body=%s", rec.Code, rec.Body.String())
	}
	waitForFaceStatus(t, client, libID, func(s ml.FaceStatus) bool { return s.People == 1 })

	// Purge removes all faces but keeps Mom.
	rec = client.roundTrip(t, http.MethodPost, "/api/v1/libraries/"+libID+"/ml/faces/purge", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("purge = %d body=%s", rec.Code, rec.Body.String())
	}
	st = waitForFaceStatus(t, client, libID, func(s ml.FaceStatus) bool { return s.Faces == 0 })
	if st.People != 1 {
		t.Errorf("after purge people = %d, want 1", st.People)
	}
	rec = client.roundTrip(t, http.MethodGet, "/api/v1/libraries/"+libID+"/people", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.People) != 1 || list.People[0].Name != "Mom" {
		t.Errorf("post-purge people = %+v, want [Mom]", list.People)
	}
}
