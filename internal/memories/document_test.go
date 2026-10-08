package memories_test

import (
	"database/sql"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Jishnu-Prasad888/Cairn/internal/librarydb"
	"github.com/Jishnu-Prasad888/Cairn/internal/memories"
)

// testLibrary is a temporary library root with an open library database.
type testLibrary struct {
	root     string
	cairnDir string
	db       *sql.DB
	store    *memories.MemoryStore
}

func newTestLibrary(t *testing.T) *testLibrary {
	t.Helper()
	root := t.TempDir()
	cairnDir := filepath.Join(root, ".cairn")
	if err := os.MkdirAll(cairnDir, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := librarydb.Open(cairnDir)
	if err != nil {
		t.Fatalf("open library db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &testLibrary{root: root, cairnDir: cairnDir, db: db, store: memories.NewMemoryStore(db)}
}

func (l *testLibrary) deriver() *memories.Deriver {
	return &memories.Deriver{LibraryRoot: l.root, CairnDir: l.cairnDir}
}

// addPhoto writes a real JPEG (w×h, a horizontal gradient so crops and
// rotations are observable) under the library root and indexes it.
func (l *testLibrary) addPhoto(t *testing.T, id, rel string, w, h int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 255 / max(w-1, 1)), uint8(y * 255 / max(h-1, 1)), 90, 255})
		}
	}
	abs := filepath.Join(l.root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(abs)
	if err != nil {
		t.Fatal(err)
	}
	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	l.index(t, id, rel, "photo", "hash-"+id)
	return abs
}

func (l *testLibrary) index(t *testing.T, id, rel, mediaType, hash string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := l.db.Exec(`INSERT INTO indexed_files
		(id, rel_path, size_bytes, mod_time, content_hash, media_type, status, first_seen_at, last_seen_at, indexed_at)
		VALUES (?, ?, 1, ?, ?, ?, 'present', ?, ?, ?)`, id, rel, now, hash, mediaType, now, now, now); err != nil {
		t.Fatalf("index %s: %v", rel, err)
	}
}

func text(md string) *memories.Block {
	return &memories.Block{Type: memories.BlockText, Markdown: md}
}

func images(layout memories.Layout, fileIDs ...string) *memories.Block {
	b := &memories.Block{Type: memories.BlockImage, Layout: layout, Images: []*memories.Image{}}
	for _, id := range fileIDs {
		b.Images = append(b.Images, &memories.Image{SourceFileID: id})
	}
	return b
}

func intPtr(v int) *int { return &v }

func TestDocumentRoundTripKeepsOrderAndIDs(t *testing.T) {
	lib := newTestLibrary(t)
	lib.addPhoto(t, "f1", "trip/a.jpg", 8, 6)
	lib.addPhoto(t, "f2", "trip/b.jpg", 8, 6)

	m, err := lib.store.Create(ctx(t), memories.CreateParams{Title: "Kerala"})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := lib.store.SaveDocument(ctx(t), m.ID, intPtr(m.Revision), []*memories.Block{
		text("# My Trip to Kerala"),
		images(memories.LayoutFeatured, "f1", "f2"),
		text("The next morning…"),
	})
	if err != nil {
		t.Fatalf("SaveDocument: %v", err)
	}
	if len(doc.Blocks) != 3 || doc.Blocks[1].Type != memories.BlockImage || len(doc.Blocks[1].Images) != 2 {
		t.Fatalf("blocks = %+v", doc.Blocks)
	}
	if doc.Blocks[1].Layout != memories.LayoutFeatured || doc.Blocks[1].Images[1].SourceFileID != "f2" {
		t.Fatalf("image block = %+v", doc.Blocks[1])
	}
	if doc.Body != "# My Trip to Kerala\n\nThe next morning…" {
		t.Errorf("legacy body = %q", doc.Body)
	}

	// Reorder: move the image block to the end and swap its images. IDs are
	// stable, so the same rows are updated.
	b := doc.Blocks
	b[1].Images[0], b[1].Images[1] = b[1].Images[1], b[1].Images[0]
	reordered := []*memories.Block{b[0], b[2], b[1]}
	ids := []string{b[0].ID, b[2].ID, b[1].ID}
	imgIDs := []string{b[1].Images[0].ID, b[1].Images[1].ID}

	doc2, err := lib.store.SaveDocument(ctx(t), m.ID, intPtr(doc.Revision), reordered)
	if err != nil {
		t.Fatal(err)
	}
	got, err := lib.store.GetDocument(ctx(t), m.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i, id := range ids {
		if got.Blocks[i].ID != id || got.Blocks[i].Position != i {
			t.Fatalf("block %d = %s@%d, want %s", i, got.Blocks[i].ID, got.Blocks[i].Position, id)
		}
	}
	if got.Blocks[2].Images[0].ID != imgIDs[0] || got.Blocks[2].Images[0].SourceFileID != "f2" {
		t.Errorf("image order not persisted: %+v", got.Blocks[2].Images)
	}
	if doc2.Revision != doc.Revision+1 {
		t.Errorf("revision %d -> %d", doc.Revision, doc2.Revision)
	}
}

func TestSaveDocumentDetectsConflicts(t *testing.T) {
	lib := newTestLibrary(t)
	m, _ := lib.store.Create(ctx(t), memories.CreateParams{Title: "t", Body: "one"})
	if _, err := lib.store.SaveDocument(ctx(t), m.ID, intPtr(m.Revision), []*memories.Block{text("two")}); err != nil {
		t.Fatal(err)
	}
	// A second writer still holding the old revision must not clobber.
	_, err := lib.store.SaveDocument(ctx(t), m.ID, intPtr(m.Revision), []*memories.Block{text("stale")})
	var ce *memories.ConflictError
	if !errors.As(err, &ce) || ce.Current != m.Revision+1 {
		t.Fatalf("err = %v, want ConflictError(current=%d)", err, m.Revision+1)
	}
	got, _ := lib.store.GetDocument(ctx(t), m.ID)
	if got.Blocks[0].Markdown != "two" {
		t.Errorf("stale write landed: %q", got.Blocks[0].Markdown)
	}
}

func TestBlockIDsCannotBeStolenFromAnotherMemory(t *testing.T) {
	lib := newTestLibrary(t)
	a, _ := lib.store.Create(ctx(t), memories.CreateParams{Title: "a", Body: "mine"})
	docA, _ := lib.store.GetDocument(ctx(t), a.ID)
	b, _ := lib.store.Create(ctx(t), memories.CreateParams{Title: "b"})

	_, err := lib.store.SaveDocument(ctx(t), b.ID, nil, []*memories.Block{
		{ID: docA.Blocks[0].ID, Type: memories.BlockText, Markdown: "hijack"},
	})
	var ve *memories.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("err = %v, want ValidationError", err)
	}
	got, _ := lib.store.GetDocument(ctx(t), a.ID)
	if got.Blocks[0].Markdown != "mine" {
		t.Errorf("memory A changed: %q", got.Blocks[0].Markdown)
	}
}

func TestValidation(t *testing.T) {
	lib := newTestLibrary(t)
	lib.addPhoto(t, "f1", "a.jpg", 4, 4)
	lib.index(t, "v1", "clip.mp4", "video", "hv")
	lib.index(t, "d1", "notes.pdf", "document", "hd")
	m, _ := lib.store.Create(ctx(t), memories.CreateParams{Title: "t"})

	cases := map[string][]*memories.Block{
		"unknown layout":    {{Type: memories.BlockImage, Layout: "spiral"}},
		"unknown type":      {{Type: "quote"}},
		"unknown media":     {images(memories.LayoutGrid, "ghost")},
		"document as image": {images(memories.LayoutGrid, "d1")},
		"bad rotation": {{Type: memories.BlockImage, Images: []*memories.Image{
			{SourceFileID: "f1", Edits: memories.Edits{Rotation: 45}}}}},
		"bad filter": {{Type: memories.BlockImage, Images: []*memories.Image{
			{SourceFileID: "f1", Edits: memories.Edits{Filter: "sparkle"}}}}},
		"video edits": {{Type: memories.BlockImage, Images: []*memories.Image{
			{SourceFileID: "v1", Edits: memories.Edits{Rotation: 90}}}}},
		"interval too short": {{Type: memories.BlockImage, SlideshowInterval: intPtr(1)}},
		"duplicate ids":      {{ID: "block-aaaa", Type: memories.BlockText}, {ID: "block-aaaa", Type: memories.BlockText}},
	}
	for name, blocks := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := lib.store.SaveDocument(ctx(t), m.ID, nil, blocks)
			var ve *memories.ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("err = %v, want ValidationError", err)
			}
		})
	}
	// A video without edits is a valid image-block member.
	if _, err := lib.store.SaveDocument(ctx(t), m.ID, nil, []*memories.Block{images(memories.LayoutGrid, "v1")}); err != nil {
		t.Fatalf("video reference: %v", err)
	}
}

func TestLegacyUpdateRefusesStructuredMemory(t *testing.T) {
	lib := newTestLibrary(t)
	lib.addPhoto(t, "f1", "a.jpg", 4, 4)
	m, _ := lib.store.Create(ctx(t), memories.CreateParams{Title: "t", Body: "hello"})
	if _, err := lib.store.SaveDocument(ctx(t), m.ID, nil, []*memories.Block{text("hello"), images("", "f1")}); err != nil {
		t.Fatal(err)
	}
	_, err := lib.store.Update(ctx(t), m.ID, memories.UpdateParams{Title: "t", Body: "flattened"})
	if !errors.Is(err, memories.ErrStructured) {
		t.Fatalf("err = %v, want ErrStructured", err)
	}
	got, _ := lib.store.GetDocument(ctx(t), m.ID)
	if len(got.Blocks) != 2 {
		t.Errorf("image block was dropped")
	}
}

func TestLegacyMemoryMigratesToTextBlock(t *testing.T) {
	root := t.TempDir()
	cairnDir := filepath.Join(root, ".cairn")
	if err := os.MkdirAll(cairnDir, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := librarydb.Open(cairnDir)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a pre-v8 memory: a body, no blocks, not yet migrated.
	created := "2024-05-01T10:00:00Z"
	if _, err := db.Exec(`INSERT INTO memories (id, title, body, created_at, updated_at, blocks_migrated)
		VALUES ('legacy1', 'Old trip', 'We saw [[media:abc|the cove]] and **the pier**.', ?, ?, 0)`,
		created, created); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	db, err = librarydb.Open(cairnDir) // the upgrade runs on open
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	store := memories.NewMemoryStore(db)
	got, err := store.GetDocument(ctx(t), "legacy1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Blocks) != 1 || got.Blocks[0].Type != memories.BlockText ||
		got.Blocks[0].Markdown != "We saw [[media:abc|the cove]] and **the pier**." {
		t.Fatalf("migrated blocks = %+v", got.Blocks)
	}
	if got.Title != "Old trip" || got.UpdatedAt.Format(time.RFC3339) != created {
		t.Errorf("metadata changed: %q %v", got.Title, got.UpdatedAt)
	}
	// Still searchable after the FTS trigger swap.
	res, _, err := store.Search(ctx(t), "pier", "", 10)
	if err != nil || len(res) != 1 {
		t.Fatalf("search after migration = %v, %v", res, err)
	}
	// Re-opening is a no-op (no duplicate block).
	db2, _ := librarydb.Open(cairnDir)
	_ = db2.Close()
	got, _ = store.GetDocument(ctx(t), "legacy1")
	if len(got.Blocks) != 1 {
		t.Errorf("blocks after reopen = %d", len(got.Blocks))
	}
}

func TestSearchIndexesCaptionsAndTags(t *testing.T) {
	lib := newTestLibrary(t)
	lib.addPhoto(t, "f1", "a.jpg", 4, 4)
	m, _ := lib.store.Create(ctx(t), memories.CreateParams{Title: "Kerala"})
	blk := images(memories.LayoutGrid, "f1")
	blk.Images[0].Caption = "Tea plantations after the rain"
	if _, err := lib.store.SaveDocument(ctx(t), m.ID, nil, []*memories.Block{text("Munnar"), blk}); err != nil {
		t.Fatal(err)
	}
	tags := []string{"monsoon"}
	loc := "Idukki"
	if _, err := lib.store.PatchMeta(ctx(t), m.ID, nil, memories.MetaPatch{Tags: &tags, Location: &loc}); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"plantations", "monsoon", "Idukki", "Munnar"} {
		res, _, err := lib.store.Search(ctx(t), q, "", 10)
		if err != nil || len(res) != 1 {
			t.Errorf("search %q = %d results, err %v", q, len(res), err)
		}
	}
	got, _ := lib.store.Get(ctx(t), m.ID)
	if len(got.Tags) != 1 || got.Tags[0] != "monsoon" || got.Location != "Idukki" {
		t.Errorf("metadata = %+v", got)
	}
}

func TestCaptionsAreMemorySpecific(t *testing.T) {
	lib := newTestLibrary(t)
	lib.addPhoto(t, "f1", "a.jpg", 4, 4)
	m1, _ := lib.store.Create(ctx(t), memories.CreateParams{Title: "one"})
	m2, _ := lib.store.Create(ctx(t), memories.CreateParams{Title: "two"})
	b1 := images("", "f1")
	b1.Images[0].Caption = "Morning"
	b2 := images("", "f1")
	b2.Images[0].Caption = "Evening"
	if _, err := lib.store.SaveDocument(ctx(t), m1.ID, nil, []*memories.Block{b1}); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.store.SaveDocument(ctx(t), m2.ID, nil, []*memories.Block{b2}); err != nil {
		t.Fatal(err)
	}
	d1, _ := lib.store.GetDocument(ctx(t), m1.ID)
	d2, _ := lib.store.GetDocument(ctx(t), m2.ID)
	if d1.Blocks[0].Images[0].Caption != "Morning" || d2.Blocks[0].Images[0].Caption != "Evening" {
		t.Fatalf("captions = %q / %q", d1.Blocks[0].Images[0].Caption, d2.Blocks[0].Images[0].Caption)
	}
	// The library's own note for the file is untouched.
	var notes int
	_ = lib.db.QueryRow(`SELECT COUNT(*) FROM file_notes WHERE file_id = 'f1'`).Scan(&notes)
	if notes != 0 {
		t.Errorf("memory caption leaked into file_notes")
	}
}

func TestMissingMediaKeepsPlaceAndReattachesByHash(t *testing.T) {
	lib := newTestLibrary(t)
	lib.addPhoto(t, "f1", "trip/a.jpg", 4, 4)
	m, _ := lib.store.Create(ctx(t), memories.CreateParams{Title: "t"})
	blk := images(memories.LayoutHero, "f1")
	blk.Images[0].Caption = "kept"
	doc, err := lib.store.SaveDocument(ctx(t), m.ID, nil, []*memories.Block{text("before"), blk})
	if err != nil {
		t.Fatal(err)
	}
	imageID := doc.Blocks[1].Images[0].ID

	// The original disappears from the index.
	if _, err := lib.db.Exec(`UPDATE indexed_files SET status = 'missing' WHERE id = 'f1'`); err != nil {
		t.Fatal(err)
	}
	got, err := lib.store.GetDocument(ctx(t), m.ID)
	if err != nil {
		t.Fatal(err)
	}
	img := got.Blocks[1].Images[0]
	if img.ID != imageID || img.Caption != "kept" || got.Blocks[1].Position != 1 {
		t.Fatalf("missing media lost its reference: %+v", img)
	}
	srcs, _ := lib.store.LookupSources(ctx(t), []string{"f1"})
	if srcs["f1"].Available() {
		t.Fatal("missing file reported available")
	}
	// Saving the document unchanged still works while the original is gone.
	if _, err := lib.store.SaveDocument(ctx(t), m.ID, nil, got.Blocks); err != nil {
		t.Fatalf("save with missing media: %v", err)
	}

	// The same bytes reappear elsewhere (moved, re-indexed under a new id).
	lib.index(t, "f1-moved", "archive/a.jpg", "photo", "hash-f1")
	changed, err := lib.store.ReconcileSources(ctx(t), m.ID)
	if err != nil || !changed {
		t.Fatalf("reconcile = %v, %v", changed, err)
	}
	got, _ = lib.store.GetDocument(ctx(t), m.ID)
	img = got.Blocks[1].Images[0]
	if img.SourceFileID != "f1-moved" || img.ID != imageID || img.Caption != "kept" {
		t.Errorf("reattached image = %+v", img)
	}
}

func TestVersionsCoalesceRapidSaves(t *testing.T) {
	lib := newTestLibrary(t)
	m, _ := lib.store.Create(ctx(t), memories.CreateParams{Title: "t"})
	for i := range 5 {
		if _, err := lib.store.SaveDocument(ctx(t), m.ID, nil, []*memories.Block{text(fmt.Sprint("draft ", i))}); err != nil {
			t.Fatal(err)
		}
	}
	vs, err := lib.store.ListVersions(ctx(t), m.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Creation version + one coalesced autosave version.
	if len(vs) != 2 || vs[0].Body != "draft 4" || vs[1].Version != 1 {
		t.Fatalf("versions = %+v", vs)
	}
	blocks, err := memories.DecodeSnapshot(vs[0].Document, vs[0].Body)
	if err != nil || len(blocks) != 1 || blocks[0].Markdown != "draft 4" {
		t.Errorf("snapshot = %+v, %v", blocks, err)
	}
}

func TestLargeMemory(t *testing.T) {
	lib := newTestLibrary(t)
	for i := range 120 {
		lib.index(t, fmt.Sprintf("file-%03d", i), fmt.Sprintf("big/%03d.jpg", i), "photo", fmt.Sprint("h", i))
	}
	m, _ := lib.store.Create(ctx(t), memories.CreateParams{Title: "A long year"})
	var blocks []*memories.Block
	for i := range 120 {
		blocks = append(blocks, text(strings.Repeat(fmt.Sprintf("Paragraph %d. ", i), 20)))
		if i%10 == 0 {
			var ids []string
			for j := i; j < i+10; j++ {
				ids = append(ids, fmt.Sprintf("file-%03d", j))
			}
			blocks = append(blocks, images(memories.LayoutMasonry, ids...))
		}
	}
	start := time.Now()
	doc, err := lib.store.SaveDocument(ctx(t), m.ID, nil, blocks)
	if err != nil {
		t.Fatal(err)
	}
	// A typical autosave: one block changed in a large document.
	doc.Blocks[50].Markdown += " edited"
	if _, err := lib.store.SaveDocument(ctx(t), m.ID, nil, doc.Blocks); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	got, _ := lib.store.GetDocument(ctx(t), m.ID)
	imageCount := 0
	for _, b := range got.Blocks {
		imageCount += len(b.Images)
	}
	if len(got.Blocks) != 132 || imageCount != 120 {
		t.Fatalf("blocks=%d images=%d", len(got.Blocks), imageCount)
	}
	if elapsed > 10*time.Second {
		t.Errorf("saving a large memory took %v", elapsed)
	}
}
