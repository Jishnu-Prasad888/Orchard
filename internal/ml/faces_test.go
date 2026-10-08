package ml_test

import (
	"context"
	"database/sql"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Jishnu-Prasad888/Cairn/internal/librarydb"
	"github.com/Jishnu-Prasad888/Cairn/internal/ml"
	_ "modernc.org/sqlite"
)

// fakeFaceProvider detects the single brightest point of an image, reporting
// a 24x24 box around it, and embeds a one-hot vector keyed on that position.
// Two images with a bright point at the same position share a descriptor and
// so cluster together; a bright point elsewhere becomes a different person.
type fakeFaceProvider struct{}

func (fakeFaceProvider) Name() string     { return "fake_faces" }
func (fakeFaceProvider) Version() int     { return 1 }
func (fakeFaceProvider) Describe() string { return "test provider" }

// brightPoint scans for the brightest pixel location in an image.
func brightPoint(img image.Image) (int, int, bool) {
	b := img.Bounds()
	bestX, bestY := b.Min.X, b.Min.Y
	var best uint32
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			lum := (r*299 + g*587 + bl*114) >> 10
			if lum > best {
				best = lum
				bestX, bestY = x, y
			}
		}
	}
	if best == 0 {
		return 0, 0, false
	}
	return bestX, bestY, true
}

func (fakeFaceProvider) Detect(img image.Image) ([]ml.FaceBox, error) {
	cx, cy, ok := brightPoint(img)
	if !ok {
		return nil, nil
	}
	return []ml.FaceBox{{X: cx - 12, Y: cy - 12, Width: 24, Height: 24, Confidence: 0.5}}, nil
}

func (fakeFaceProvider) Embed(img image.Image, _ ml.FaceBox) ([]float32, error) {
	cx, cy, ok := brightPoint(img)
	if !ok {
		return make([]float32, 256), nil
	}
	b := img.Bounds()
	xb := ((cx - b.Min.X) * 16) / b.Dx()
	yb := ((cy - b.Min.Y) * 16) / b.Dy()
	if xb > 15 {
		xb = 15
	}
	if yb > 15 {
		yb = 15
	}
	v := make([]float32, 256)
	v[yb*16+xb] = 1
	return v, nil
}

func TestFaceProviderDetectBlank(t *testing.T) {
	p := ml.NewPigoFaceProvider(0, 0)
	if p.Name() == "" || p.Version() != 1 {
		t.Fatalf("provider identity invalid: %+v", p)
	}
	img := image.NewGray(image.Rect(0, 0, 320, 240))
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			img.SetGray(x, y, color.Gray{Y: 10})
		}
	}
	faces, err := p.Detect(img)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(faces) != 0 {
		t.Errorf("Detect on blank image = %d faces, want 0", len(faces))
	}
}

func TestFaceProviderEmbedDeterministic(t *testing.T) {
	p := ml.NewPigoFaceProvider(0, 0)
	img := image.NewGray(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			img.SetGray(x, y, color.Gray{Y: uint8(x*4 + y)})
		}
	}
	box := ml.FaceBox{X: 20, Y: 12, Width: 24, Height: 24, Confidence: 0.9}
	a, err := p.Embed(img, box)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(a) != 256 {
		t.Fatalf("descriptor length = %d, want 256", len(a))
	}
	b, err := p.Embed(img, box)
	if err != nil {
		t.Fatalf("Embed 2nd: %v", err)
	}
	sim := ml.DescriptorCosine(a, b)
	if sim < 0.999 {
		t.Errorf("self cosine = %f, want ~1", sim)
	}
	jpegBytes, err := ml.FaceJPEG(img, box, 0)
	if err != nil {
		t.Fatalf("FaceJPEG: %v", err)
	}
	if len(jpegBytes) == 0 {
		t.Error("FaceJPEG returned empty bytes")
	}
}

func TestFaceStoreLifecycle(t *testing.T) {
	root := t.TempDir()
	seedFiles(t, root, []imgSeed{{id: "file0", rel: "img0.png"}})

	db, err := librarydb.OpenDB(filepath.Join(root, ".cairn"))
	if err != nil {
		t.Fatal(err)
	}
	store := ml.NewFaceStore(db.DB())
	ctx := context.Background()

	f1 := ml.FaceRecord{ID: "face1", FileID: "file0", Provider: "p", Version: 1,
		Box:        ml.FaceBox{X: 1, Y: 2, Width: 24, Height: 24, Confidence: 0.9},
		Descriptor: []float32{1, 0, 0}}
	f2 := ml.FaceRecord{ID: "face2", FileID: "file0", Provider: "p", Version: 1,
		Box:        ml.FaceBox{X: 50, Y: 50, Width: 24, Height: 24, Confidence: 0.8},
		Descriptor: []float32{0, 1, 0}}
	if err := store.InsertFace(ctx, f1); err != nil {
		t.Fatalf("InsertFace: %v", err)
	}
	if err := store.InsertFace(ctx, f2); err != nil {
		t.Fatalf("InsertFace 2: %v", err)
	}

	p, err := store.CreatePerson(ctx, "Mom")
	if err != nil {
		t.Fatalf("CreatePerson: %v", err)
	}
	if err := store.AssignPerson(ctx, p.ID, f1.ID, "manual"); err != nil {
		t.Fatalf("AssignPerson: %v", err)
	}
	if err := store.RenamePerson(ctx, p.ID, "Mum"); err != nil {
		t.Fatalf("RenamePerson: %v", err)
	}
	if err := store.SetCover(ctx, p.ID, f1.ID); err != nil {
		t.Fatalf("SetCover: %v", err)
	}

	people, err := store.ListPeople(ctx)
	if err != nil || len(people) != 1 {
		t.Fatalf("ListPeople = %+v, %v; want 1", people, err)
	}
	if people[0].FaceCount != 1 || people[0].CoverFaceID != "face1" {
		t.Errorf("person = %+v; want FaceCount 1 cover face1", people[0])
	}
	if people[0].Name != "Mum" {
		t.Errorf("renamed person = %q, want Mum", people[0].Name)
	}

	unassigned, err := store.FacesUnassigned(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(unassigned) != 1 || unassigned[0].ID != "face2" {
		t.Errorf("unassigned = %+v; want only face2", unassigned)
	}

	// A face can belong to only one person; assigning moves it.
	if err := store.AssignPerson(ctx, p.ID, f2.ID, "auto"); err != nil {
		t.Fatal(err)
	}
	pf, err := store.PersonFaces(ctx, p.ID)
	if err != nil || len(pf) != 2 {
		t.Fatalf("PersonFaces = %+v, %v; want 2", pf, err)
	}
	if err := store.UnassignPerson(ctx, p.ID, f2.ID); err != nil {
		t.Fatalf("UnassignPerson: %v", err)
	}
	people, _ = store.ListPeople(ctx)
	if people[0].FaceCount != 1 {
		t.Errorf("after unassign FaceCount = %d, want 1", people[0].FaceCount)
	}

	// Purge keeps people, drops faces.
	n, err := store.PurgeFaces(ctx)
	if err != nil || n != 2 {
		t.Fatalf("PurgeFaces = %d, %v; want 2", n, err)
	}
	people, _ = store.ListPeople(ctx)
	if len(people) != 1 || people[0].CoverFaceID != "" {
		t.Errorf("after purge people = %+v; want 1 with no cover", people)
	}
	_ = db.Close()
}

func TestFaceManagerPassClusterPurge(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()

	images := []struct {
		id, name string
		cx       int
	}{
		{"file0", "mom1.png", 40},
		{"file1", "mom2.png", 40},
		{"file2", "other.png", 160},
	}
	now := "2026-01-01T00:00:00.000Z"
	var files []imgSeed
	for _, im := range images {
		img := image.NewRGBA(image.Rect(0, 0, 200, 200))
		for y := 0; y < 200; y++ {
			for x := 0; x < 200; x++ {
				img.Set(x, y, color.RGBA{R: 10, G: 10, B: 10, A: 255})
			}
		}
		img.Set(im.cx, im.cx, color.RGBA{R: 250, G: 250, B: 250, A: 255})
		files = append(files, imgSeed{id: im.id, rel: im.name, now: now, img: img})
	}
	seedFiles(t, root, files)

	m := ml.NewFaceManager(tLog(), ml.FaceConfig{
		Enabled:       true,
		Workers:       2,
		Threshold:     0.9,
		MinSize:       20,
		MinConfidence: 0.5,
	}, fakeFaceProvider{})

	st, err := m.Status(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Enabled || st.Faces != 0 {
		t.Errorf("initial status = %+v", st)
	}

	n, err := m.Pass(ctx, root)
	if err != nil {
		t.Fatalf("Pass: %v", err)
	}
	if n != 3 {
		t.Errorf("Pass processed %d files, want 3", n)
	}
	if n, err := m.Pass(ctx, root); err != nil || n != 0 {
		t.Errorf("second Pass = %d, %v; want 0", n, err)
	}

	res, err := m.ClusterPass(ctx, root)
	if err != nil {
		t.Fatalf("ClusterPass: %v", err)
	}
	// The two matching faces form a person; the lone face waits under
	// "Who is this?" rather than becoming a person of its own.
	if res.Inspected != 3 || res.Assigned != 0 || res.Created != 1 {
		t.Errorf("ClusterPass = %+v; want inspected 3 assigned 0 created 1", res)
	}

	people, err := m.People(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(people) != 1 || people[0].FaceCount != 2 {
		t.Fatalf("people = %+v; want one person with 2 faces", people)
	}
	if un, _ := m.Unassigned(ctx, root); len(un) != 1 {
		t.Errorf("unassigned = %d, want 1", len(un))
	}
	first := people[0].ID

	// Regrouping without new faces changes nothing and keeps the person's id.
	res2, err := m.ClusterPass(ctx, root)
	if err != nil || res2.Created != 0 || res2.Assigned != 0 {
		t.Errorf("second ClusterPass = %+v, %v; want nothing created or moved", res2, err)
	}
	people, _ = m.People(ctx, root)
	if len(people) != 1 || people[0].ID != first {
		t.Fatalf("after regroup people = %+v; want the same single person %s", people, first)
	}
	if err := m.RenamePerson(ctx, root, first, "Mom"); err != nil {
		t.Fatal(err)
	}

	mOff := ml.NewFaceManager(tLog(), ml.FaceConfig{Enabled: false}, fakeFaceProvider{})
	if _, err := mOff.Pass(ctx, root); err == nil {
		t.Error("Pass on disabled manager: want error")
	}

	n, err = m.Purge(ctx, root)
	if err != nil || n != 3 {
		t.Errorf("Purge = %d, %v; want 3", n, err)
	}
	people, _ = m.People(ctx, root)
	if len(people) != 1 {
		t.Errorf("after purge people = %d, want 1 (names survive)", len(people))
	}
	var mom bool
	for _, p := range people {
		if p.Name == "Mom" {
			mom = true
			if p.FaceCount != 0 {
				t.Errorf("post-purge Mom FaceCount = %d, want 0", p.FaceCount)
			}
		}
	}
	if !mom {
		t.Error("renamed person Mom lost after purge")
	}
}

type imgSeed struct {
	id, rel, now string
	img          image.Image
}

// seedFiles creates the .cairn dir (when missing), writes any provided files,
// and records an indexed_files + photo media_metadata row for each.
func seedFiles(t *testing.T, root string, files []imgSeed) {
	t.Helper()
	cairnDir := filepath.Join(root, ".cairn")
	if err := os.MkdirAll(cairnDir, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := librarydb.OpenDB(cairnDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	now := "2026-01-01T00:00:00.000Z"
	for _, f := range files {
		if f.img != nil {
			writePNGImage(t, filepath.Join(root, f.rel), f.img)
		}
		rel := f.rel
		if rel == "" {
			rel = "img0.png"
		}
		n := f.now
		if n == "" {
			n = now
		}
		if _, err := db.DB().ExecContext(ctx, `
			INSERT INTO indexed_files (id, rel_path, size_bytes, mod_time, status, first_seen_at, last_seen_at, indexed_at)
			VALUES (?, ?, 1, ?, 'present', ?, ?, ?)`, f.id, rel, n, n, n, n); err != nil {
			t.Fatal(err)
		}
		if _, err := db.DB().ExecContext(ctx, `
			INSERT INTO media_metadata (file_id, media_type, updated_at) VALUES (?, 'photo', ?)`, f.id, n); err != nil {
			t.Fatal(err)
		}
	}
}

func writePNGImage(t *testing.T, path string, img image.Image) {
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

// The post-scan hook runs before media-metadata jobs have written anything,
// so a freshly scanned photo must already be eligible for face detection.
func TestFilesToScanDoesNotNeedMediaMetadata(t *testing.T) {
	root := t.TempDir()
	cairnDir := filepath.Join(root, ".cairn")
	if err := os.MkdirAll(cairnDir, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := librarydb.OpenDB(cairnDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	n := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.DB().Exec(`
		INSERT INTO indexed_files (id, rel_path, size_bytes, mod_time, status, media_type, first_seen_at, last_seen_at, indexed_at)
		VALUES ('p1', 'a.jpg', 1, ?, 'present', 'photo', ?, ?, ?),
		       ('v1', 'a.mp4', 1, ?, 'present', 'video', ?, ?, ?)`, n, n, n, n, n, n, n, n); err != nil {
		t.Fatal(err)
	}
	files, err := ml.NewFaceStore(db.DB()).FilesToScan(context.Background(), "pigo", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].FileID != "p1" {
		t.Fatalf("FilesToScan = %+v, want only the photo p1", files)
	}
}

// One photo that cannot be decoded must not stop the pass, and photos already
// analysed (with or without faces) are not analysed again.
func TestFacePassSkipsBrokenPhotosAndRemembersScans(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	blank := image.NewRGBA(image.Rect(0, 0, 64, 64))
	face := image.NewRGBA(image.Rect(0, 0, 200, 200))
	face.Set(40, 40, color.RGBA{R: 250, G: 250, B: 250, A: 255})
	seedFiles(t, root, []imgSeed{
		{id: "a", rel: "a.png", img: face},
		{id: "b", rel: "broken.png"},
		{id: "c", rel: "blank.png", img: blank},
	})
	if err := os.WriteFile(filepath.Join(root, "broken.png"), []byte("not an image"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := ml.NewFaceManager(tLog(), ml.FaceConfig{Enabled: true, Workers: 1, MinSize: 20}, fakeFaceProvider{})
	n, err := m.Pass(ctx, root)
	if err != nil || n != 3 {
		t.Fatalf("Pass = %d, %v; want 3 files, no error", n, err)
	}
	if st, _ := m.Status(ctx, root); st.Faces != 1 || st.Running {
		t.Errorf("status = %+v; want 1 face, not running", st)
	}
	if n, err := m.Pass(ctx, root); err != nil || n != 0 {
		t.Errorf("second Pass = %d, %v; want nothing left to scan", n, err)
	}
}

// Moving a person's cover face to someone else re-picks the old person's
// cover, gives the new person one, and removes an automatic group left empty.
func TestAssignPersonKeepsCoversAndDropsEmptyGroups(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	seedFiles(t, root, []imgSeed{{id: "f1", rel: "a.png"}, {id: "f2", rel: "b.png"}})
	db, err := librarydb.OpenDB(filepath.Join(root, ".cairn"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	store := ml.NewFaceStore(db.DB())
	for i, id := range []string{"a", "b", "c"} {
		if err := store.InsertFace(ctx, ml.FaceRecord{
			ID: id, FileID: []string{"f1", "f2", "f2"}[i], Provider: "p", Version: 1,
			Box:        ml.FaceBox{Width: 50 + i*10, Height: 50, Confidence: 0.9},
			Descriptor: []float32{1, 0},
		}); err != nil {
			t.Fatal(err)
		}
	}
	auto, _ := store.CreatePerson(ctx, "Person 1")
	solo, _ := store.CreatePerson(ctx, "Person 2")
	asha, _ := store.CreatePerson(ctx, "Asha")
	for _, a := range []struct{ p, f string }{{auto.ID, "a"}, {auto.ID, "b"}, {solo.ID, "c"}} {
		if err := store.AssignPerson(ctx, a.p, a.f, "auto"); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SetCover(ctx, auto.ID, "a"); err != nil {
		t.Fatal(err)
	}

	if err := store.AssignPerson(ctx, asha.ID, "a", "manual"); err != nil {
		t.Fatal(err)
	}
	if err := store.AssignPerson(ctx, asha.ID, "c", "manual"); err != nil {
		t.Fatal(err)
	}
	people, err := store.ListPeople(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]ml.Person{}
	for _, p := range people {
		got[p.Name] = p
	}
	if p := got["Person 1"]; p.FaceCount != 1 || p.CoverFaceID != "b" {
		t.Errorf("Person 1 = %+v; want 1 face with cover b", p)
	}
	if p := got["Asha"]; p.FaceCount != 2 || (p.CoverFaceID != "a" && p.CoverFaceID != "c") {
		t.Errorf("Asha = %+v; want 2 faces and a cover of her own", p)
	}
	if _, ok := got["Person 2"]; ok {
		t.Error("empty automatic Person 2 should have been removed")
	}
}

// "Not this person" holds the face back from automatic grouping, a deleted
// face is not detected again, and deleting a person with their faces keeps the
// group from coming back.
func TestHoldsAndDeletesStick(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	var files []imgSeed
	for i, name := range []string{"a.png", "b.png", "c.png"} {
		img := image.NewRGBA(image.Rect(0, 0, 200, 200))
		img.Set(40, 40, color.RGBA{R: 250, G: 250, B: 250, A: 255})
		files = append(files, imgSeed{id: string(rune('a' + i)), rel: name, img: img})
	}
	seedFiles(t, root, files)
	m := ml.NewFaceManager(tLog(), ml.FaceConfig{Enabled: true, Workers: 1, MinSize: 20, Threshold: 0.9}, fakeFaceProvider{})
	if _, err := m.Pass(ctx, root); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ClusterPass(ctx, root); err != nil {
		t.Fatal(err)
	}
	people, _ := m.People(ctx, root)
	if len(people) != 1 || people[0].FaceCount != 3 {
		t.Fatalf("people = %+v; want one person with 3 faces", people)
	}
	faces, _ := m.PersonFaces(ctx, root, people[0].ID)

	// Not this person: the face stays out after regrouping.
	if err := m.UnassignFace(ctx, root, people[0].ID, faces[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ClusterPass(ctx, root); err != nil {
		t.Fatal(err)
	}
	if un, _ := m.Unassigned(ctx, root); len(un) != 1 || un[0].ID != faces[0].ID {
		t.Fatalf("unassigned = %+v; want the held face only", un)
	}

	// Deleting it removes it for good: the next detection pass skips the photo.
	if err := m.DeleteFace(ctx, root, faces[0].ID); err != nil {
		t.Fatal(err)
	}
	if n, err := m.Pass(ctx, root); err != nil || n != 0 {
		t.Errorf("Pass after delete = %d, %v; want nothing re-detected", n, err)
	}
	if st, _ := m.Status(ctx, root); st.Faces != 2 {
		t.Errorf("faces = %d, want 2", st.Faces)
	}

	// Deleting the person with their faces leaves nothing to regroup.
	if n, err := m.DeletePersonAndFaces(ctx, root, people[0].ID); err != nil || n != 2 {
		t.Fatalf("DeletePersonAndFaces = %d, %v; want 2", n, err)
	}
	if _, err := m.ClusterPass(ctx, root); err != nil {
		t.Fatal(err)
	}
	if st, _ := m.Status(ctx, root); st.Faces != 0 || st.People != 0 {
		t.Errorf("status = %+v; want no faces and no people", st)
	}
}

// Images wait until a full batch has piled up, however they arrived (the
// indexer cannot tell uploads from files moved in by hand), and the stored
// batch size survives a restart.
func TestRuntimeRunsModelsOnlyOnceABatchIsWaiting(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE server_settings (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	newRuntime := func() *ml.Runtime {
		fm := ml.NewFaceManager(tLog(), ml.FaceConfig{Enabled: true, Workers: 1, MinSize: 20}, fakeFaceProvider{})
		rt := ml.NewRuntime(db, tLog(), ml.NewManager(tLog(), ml.Config{}, nil), fm, false,
			func(context.Context) ([]ml.Target, error) {
				return []ml.Target{{ID: "lib", Root: root, Online: true}}, nil
			})
		if err := rt.Load(ctx, true); err != nil {
			t.Fatal(err)
		}
		return rt
	}
	rt := newRuntime()
	if err := rt.SetBatchSize(ctx, 3); err != nil {
		t.Fatal(err)
	}

	blank := image.NewRGBA(image.Rect(0, 0, 64, 64))
	seedFiles(t, root, []imgSeed{{id: "a", rel: "a.png", img: blank}, {id: "b", rel: "b.png", img: blank}})
	rt.AfterScan("lib", root)
	time.Sleep(300 * time.Millisecond)
	if n, _ := rt.Pending(ctx, root); n != 2 {
		t.Fatalf("pending = %d after 2 images with batch 3; want them left waiting", n)
	}

	// Restart: the batch size and the waiting images are both still there.
	rt = newRuntime()
	if rt.BatchSize() != 3 {
		t.Fatalf("batch size after restart = %d, want 3", rt.BatchSize())
	}
	seedFiles(t, root, []imgSeed{{id: "c", rel: "c.png", img: blank}})
	rt.AfterScan("lib", root)
	waitFor(t, func() bool { n, _ := rt.Pending(ctx, root); return n == 0 })

	// Lowering the batch size releases images that were waiting.
	seedFiles(t, root, []imgSeed{{id: "d", rel: "d.png", img: blank}})
	rt.AfterScan("lib", root)
	time.Sleep(300 * time.Millisecond)
	if n, _ := rt.Pending(ctx, root); n != 1 {
		t.Fatalf("pending = %d, want 1 waiting", n)
	}
	if err := rt.SetBatchSize(ctx, 1); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { n, _ := rt.Pending(ctx, root); return n == 0 })
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
