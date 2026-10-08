package memories_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image/jpeg"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/Jishnu-Prasad888/Cairn/internal/memories"
)

func sha256File(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

type fileStamp struct {
	hash string
	info os.FileInfo
}

func stamp(t *testing.T, path string) fileStamp {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fileStamp{hash: sha256File(t, path), info: info}
}

func (s fileStamp) assertUnchanged(t *testing.T, path string) {
	t.Helper()
	now := stamp(t, path)
	if now.hash != s.hash {
		t.Fatalf("ORIGINAL MODIFIED: sha256 %s -> %s", s.hash, now.hash)
	}
	if !now.info.ModTime().Equal(s.info.ModTime()) || now.info.Size() != s.info.Size() || now.info.Mode() != s.info.Mode() {
		t.Fatalf("original metadata changed: %v/%d/%v -> %v/%d/%v", s.info.ModTime(), s.info.Size(), s.info.Mode(),
			now.info.ModTime(), now.info.Size(), now.info.Mode())
	}
}

func editedImageBlock(fileID string, e memories.Edits) *memories.Block {
	return &memories.Block{Type: memories.BlockImage, Layout: memories.LayoutHero,
		Images: []*memories.Image{{SourceFileID: fileID, Edits: e}}}
}

func derivedFiles(t *testing.T, lib *testLibrary) []string {
	t.Helper()
	var out []string
	root := filepath.Join(lib.cairnDir, memories.DerivedDirName)
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && filepath.Ext(p) == ".jpg" {
			out = append(out, p)
		}
		return nil
	})
	return out
}

// TestOriginalIntegrity is the critical integrity test: a library image's
// SHA-256 (and size, mode, mtime) is identical before and after it is added
// to a memory, edited every way the editor allows, rendered into an edited
// copy, reset, and removed.
func TestOriginalIntegrity(t *testing.T) {
	lib := newTestLibrary(t)
	orig := lib.addPhoto(t, "f1", "2024/kerala/IMG_0001.jpg", 64, 48)
	before := stamp(t, orig)

	m, _ := lib.store.Create(ctx(t), memories.CreateParams{Title: "Kerala"})
	doc, err := lib.store.SaveDocument(ctx(t), m.ID, nil, []*memories.Block{editedImageBlock("f1", memories.Edits{})})
	if err != nil {
		t.Fatal(err)
	}
	before.assertUnchanged(t, orig)

	blk := doc.Blocks[0]
	blk.Images[0].Caption = "The road to Munnar"
	blk.Images[0].Edits = memories.Edits{
		Crop:        &memories.Crop{X: 0.1, Y: 0.1, Width: 0.5, Height: 0.6},
		Rotation:    90,
		Filter:      memories.FilterWarm,
		Adjustments: memories.Adjustments{Brightness: 10, Contrast: -5, Saturation: 20},
	}
	if _, err := lib.store.SaveDocument(ctx(t), m.ID, nil, []*memories.Block{blk}); err != nil {
		t.Fatal(err)
	}
	res, err := lib.store.SyncDerived(ctx(t), m.ID, lib.deriver(), true)
	if err != nil || res.Created != 1 {
		t.Fatalf("SyncDerived = %+v, %v", res, err)
	}
	before.assertUnchanged(t, orig)

	doc, _ = lib.store.GetDocument(ctx(t), m.ID)
	doc.Blocks[0].Images[0].Edits = memories.Edits{}
	if _, err := lib.store.SaveDocument(ctx(t), m.ID, nil, doc.Blocks); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.store.SyncDerived(ctx(t), m.ID, lib.deriver(), true); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.store.SaveDocument(ctx(t), m.ID, nil, []*memories.Block{}); err != nil {
		t.Fatal(err)
	}
	if err := lib.store.Delete(ctx(t), m.ID); err != nil {
		t.Fatal(err)
	}
	before.assertUnchanged(t, orig)

	// Nothing but the original lives in the user's folder.
	entries, _ := os.ReadDir(filepath.Dir(orig))
	if len(entries) != 1 {
		t.Errorf("user folder gained files: %v", entries)
	}
}

// TestDerivedLifecycle: original → memory → crop → save → derived copy
// created in .cairn/memory-media → original unchanged → memory references the
// derived copy; then reset → memory references the original and the copy is
// collected.
func TestDerivedLifecycle(t *testing.T) {
	lib := newTestLibrary(t)
	orig := lib.addPhoto(t, "f1", "a.jpg", 100, 50)
	before := stamp(t, orig)

	m, _ := lib.store.Create(ctx(t), memories.CreateParams{Title: "t"})
	crop := memories.Edits{Crop: &memories.Crop{X: 0, Y: 0, Width: 0.5, Height: 1}}
	doc, err := lib.store.SaveDocument(ctx(t), m.ID, nil, []*memories.Block{editedImageBlock("f1", crop)})
	if err != nil {
		t.Fatal(err)
	}
	imageID := doc.Blocks[0].Images[0].ID
	if _, err := lib.store.SyncDerived(ctx(t), m.ID, lib.deriver(), true); err != nil {
		t.Fatal(err)
	}

	got, _ := lib.store.GetDocument(ctx(t), m.ID)
	derivedID := got.Blocks[0].Images[0].DerivedID
	if derivedID == "" {
		t.Fatal("memory image does not reference a derived copy")
	}
	files := derivedFiles(t, lib)
	if len(files) != 1 {
		t.Fatalf("derived files = %v", files)
	}
	wantDir := filepath.Join(lib.cairnDir, memories.DerivedDirName, m.ID)
	if filepath.Dir(files[0]) != wantDir {
		t.Errorf("derived copy at %s, want under %s", files[0], wantDir)
	}
	f, err := lib.store.OpenDerived(ctx(t), lib.deriver(), m.ID, imageID)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := jpeg.DecodeConfig(bytesReader(f.Data))
	if err != nil || cfg.Width != 50 || cfg.Height != 50 {
		t.Fatalf("derived size = %dx%d (%v), want 50x50", cfg.Width, cfg.Height, err)
	}
	before.assertUnchanged(t, orig)

	// Re-syncing an up-to-date memory renders nothing new.
	res, _ := lib.store.SyncDerived(ctx(t), m.ID, lib.deriver(), true)
	if res.Created != 0 {
		t.Errorf("unchanged edits re-rendered: %+v", res)
	}

	// Reset all edits: back to the original, copy collected.
	got.Blocks[0].Images[0].Edits = memories.Edits{Filter: memories.FilterOriginal}
	if _, err := lib.store.SaveDocument(ctx(t), m.ID, nil, got.Blocks); err != nil {
		t.Fatal(err)
	}
	res, err = lib.store.SyncDerived(ctx(t), m.ID, lib.deriver(), true)
	if err != nil || res.Removed != 1 {
		t.Fatalf("reset sync = %+v, %v", res, err)
	}
	got, _ = lib.store.GetDocument(ctx(t), m.ID)
	if got.Blocks[0].Images[0].DerivedID != "" || got.Blocks[0].Images[0].SourceFileID != "f1" {
		t.Errorf("after reset image = %+v", got.Blocks[0].Images[0])
	}
	if files := derivedFiles(t, lib); len(files) != 0 {
		t.Errorf("derived files after reset = %v", files)
	}
	before.assertUnchanged(t, orig)
}

// TestNoDuplicateForUneditedImages: adding images without edits never
// creates a derived copy, even with the setting on.
func TestNoDuplicateForUneditedImages(t *testing.T) {
	lib := newTestLibrary(t)
	lib.addPhoto(t, "f1", "a.jpg", 10, 10)
	lib.addPhoto(t, "f2", "b.jpg", 10, 10)
	m, _ := lib.store.Create(ctx(t), memories.CreateParams{Title: "t"})
	if _, err := lib.store.SaveDocument(ctx(t), m.ID, nil, []*memories.Block{images(memories.LayoutGrid, "f1", "f2")}); err != nil {
		t.Fatal(err)
	}
	res, err := lib.store.SyncDerived(ctx(t), m.ID, lib.deriver(), true)
	if err != nil || res.Created != 0 {
		t.Fatalf("SyncDerived = %+v, %v", res, err)
	}
	if _, err := os.Stat(filepath.Join(lib.cairnDir, memories.DerivedDirName)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("memory-media dir created without any edits")
	}
	got, _ := lib.store.GetDocument(ctx(t), m.ID)
	for _, img := range got.Blocks[0].Images {
		if img.DerivedID != "" {
			t.Errorf("unedited image has derived copy: %+v", img)
		}
	}
}

// TestEditsOffKeepsPresentationOnly: with the setting off, edits persist as
// presentation data and no file is written.
func TestEditsOffKeepsPresentationOnly(t *testing.T) {
	lib := newTestLibrary(t)
	lib.addPhoto(t, "f1", "a.jpg", 10, 10)
	m, _ := lib.store.Create(ctx(t), memories.CreateParams{Title: "t"})
	if _, err := lib.store.SaveDocument(ctx(t), m.ID, nil,
		[]*memories.Block{editedImageBlock("f1", memories.Edits{Rotation: 180, Filter: memories.FilterBW})}); err != nil {
		t.Fatal(err)
	}
	res, err := lib.store.SyncDerived(ctx(t), m.ID, lib.deriver(), false)
	if err != nil || res.Created != 0 || len(derivedFiles(t, lib)) != 0 {
		t.Fatalf("setting off produced copies: %+v, %v", res, err)
	}
	got, _ := lib.store.GetDocument(ctx(t), m.ID)
	if e := got.Blocks[0].Images[0].Edits; e.Rotation != 180 || e.Filter != memories.FilterBW {
		t.Errorf("edits not persisted: %+v", e)
	}
}

// TestMultipleMemoriesEditIndependently: the same original cropped two ways
// in two memories yields two independent copies; changing one never affects
// the other.
func TestMultipleMemoriesEditIndependently(t *testing.T) {
	lib := newTestLibrary(t)
	orig := lib.addPhoto(t, "f1", "a.jpg", 80, 40)
	before := stamp(t, orig)
	a, _ := lib.store.Create(ctx(t), memories.CreateParams{Title: "A"})
	b, _ := lib.store.Create(ctx(t), memories.CreateParams{Title: "B"})
	cropA := memories.Edits{Crop: &memories.Crop{X: 0, Y: 0, Width: 0.25, Height: 1}}
	cropB := memories.Edits{Crop: &memories.Crop{X: 0.5, Y: 0, Width: 0.5, Height: 0.5}}
	for _, step := range []struct {
		id string
		e  memories.Edits
	}{{a.ID, cropA}, {b.ID, cropB}} {
		if _, err := lib.store.SaveDocument(ctx(t), step.id, nil, []*memories.Block{editedImageBlock("f1", step.e)}); err != nil {
			t.Fatal(err)
		}
		if _, err := lib.store.SyncDerived(ctx(t), step.id, lib.deriver(), true); err != nil {
			t.Fatal(err)
		}
	}
	size := func(memoryID string) (int, int) {
		doc, _ := lib.store.GetDocument(ctx(t), memoryID)
		f, err := lib.store.OpenDerived(ctx(t), lib.deriver(), memoryID, doc.Blocks[0].Images[0].ID)
		if err != nil {
			t.Fatalf("open derived for %s: %v", memoryID, err)
		}
		cfg, _ := jpeg.DecodeConfig(bytesReader(f.Data))
		return cfg.Width, cfg.Height
	}
	if w, h := size(a.ID); w != 20 || h != 40 {
		t.Errorf("A = %dx%d, want 20x40", w, h)
	}
	if w, h := size(b.ID); w != 40 || h != 20 {
		t.Errorf("B = %dx%d, want 40x20", w, h)
	}

	// Change A's crop; B's copy and edits are untouched.
	docA, _ := lib.store.GetDocument(ctx(t), a.ID)
	docA.Blocks[0].Images[0].Edits.Crop = &memories.Crop{X: 0, Y: 0, Width: 1, Height: 0.5}
	if _, err := lib.store.SaveDocument(ctx(t), a.ID, nil, docA.Blocks); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.store.SyncDerived(ctx(t), a.ID, lib.deriver(), true); err != nil {
		t.Fatal(err)
	}
	if w, h := size(a.ID); w != 80 || h != 20 {
		t.Errorf("A after change = %dx%d, want 80x20", w, h)
	}
	if w, h := size(b.ID); w != 40 || h != 20 {
		t.Errorf("B changed when A was edited: %dx%d", w, h)
	}
	docB, _ := lib.store.GetDocument(ctx(t), b.ID)
	if c := docB.Blocks[0].Images[0].Edits.Crop; c == nil || c.X != 0.5 {
		t.Errorf("B's crop changed: %+v", c)
	}
	before.assertUnchanged(t, orig)
}

// TestDuplicateBlockSharesOriginals: duplicating an image block copies
// references and configuration, not media.
func TestDuplicateBlockSharesOriginals(t *testing.T) {
	lib := newTestLibrary(t)
	lib.addPhoto(t, "f1", "a.jpg", 10, 10)
	lib.addPhoto(t, "f2", "b.jpg", 10, 10)
	m, _ := lib.store.Create(ctx(t), memories.CreateParams{Title: "t"})
	doc, _ := lib.store.SaveDocument(ctx(t), m.ID, nil, []*memories.Block{images(memories.LayoutTwoColumn, "f1", "f2")})
	orig := doc.Blocks[0]
	dup := &memories.Block{Type: orig.Type, Layout: orig.Layout}
	for _, img := range orig.Images {
		dup.Images = append(dup.Images, &memories.Image{SourceFileID: img.SourceFileID, Caption: img.Caption})
	}
	doc, err := lib.store.SaveDocument(ctx(t), m.ID, nil, []*memories.Block{orig, dup})
	if err != nil {
		t.Fatal(err)
	}
	if doc.Blocks[1].Images[0].SourceFileID != "f1" || doc.Blocks[1].Images[0].ID == orig.Images[0].ID {
		t.Errorf("duplicate = %+v", doc.Blocks[1].Images[0])
	}
	var files int
	_ = lib.db.QueryRow(`SELECT COUNT(*) FROM indexed_files`).Scan(&files)
	if files != 2 {
		t.Errorf("library files = %d, want 2 (no media duplicated)", files)
	}
}

// TestReservedDirCollision: Cairn never writes into, moves, or overwrites a
// pre-existing memory-media path it did not create.
func TestReservedDirCollision(t *testing.T) {
	setup := func(t *testing.T) (*testLibrary, string) {
		lib := newTestLibrary(t)
		lib.addPhoto(t, "f1", "a.jpg", 10, 10)
		m, _ := lib.store.Create(ctx(t), memories.CreateParams{Title: "t"})
		if _, err := lib.store.SaveDocument(ctx(t), m.ID, nil,
			[]*memories.Block{editedImageBlock("f1", memories.Edits{Rotation: 90})}); err != nil {
			t.Fatal(err)
		}
		return lib, m.ID
	}

	t.Run("foreign directory", func(t *testing.T) {
		lib, id := setup(t)
		foreign := filepath.Join(lib.cairnDir, memories.DerivedDirName)
		if err := os.MkdirAll(foreign, 0o755); err != nil {
			t.Fatal(err)
		}
		mine := filepath.Join(foreign, "someone-elses.txt")
		if err := os.WriteFile(mine, []byte("keep me"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := lib.store.SyncDerived(ctx(t), id, lib.deriver(), true)
		if !errors.Is(err, memories.ErrReservedPathConflict) {
			t.Fatalf("err = %v, want ErrReservedPathConflict", err)
		}
		entries, _ := os.ReadDir(foreign)
		if len(entries) != 1 {
			t.Errorf("foreign dir modified: %v", entries)
		}
		if data, _ := os.ReadFile(mine); string(data) != "keep me" {
			t.Errorf("foreign file changed")
		}
	})

	t.Run("file in the way", func(t *testing.T) {
		lib, id := setup(t)
		blocker := filepath.Join(lib.cairnDir, memories.DerivedDirName)
		if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := lib.store.SyncDerived(ctx(t), id, lib.deriver(), true); !errors.Is(err, memories.ErrReservedPathConflict) {
			t.Fatalf("err = %v", err)
		}
		if data, _ := os.ReadFile(blocker); string(data) != "x" {
			t.Errorf("blocking file changed")
		}
	})

	t.Run("symlink", func(t *testing.T) {
		lib, id := setup(t)
		target := t.TempDir()
		if err := os.Symlink(target, filepath.Join(lib.cairnDir, memories.DerivedDirName)); err != nil {
			t.Skip("symlinks unsupported:", err)
		}
		if _, err := lib.store.SyncDerived(ctx(t), id, lib.deriver(), true); !errors.Is(err, memories.ErrReservedPathConflict) {
			t.Fatalf("err = %v", err)
		}
		if entries, _ := os.ReadDir(target); len(entries) != 0 {
			t.Errorf("wrote through a symlink: %v", entries)
		}
	})

	t.Run("empty pre-existing dir is claimed", func(t *testing.T) {
		lib, id := setup(t)
		if err := os.MkdirAll(filepath.Join(lib.cairnDir, memories.DerivedDirName), 0o755); err != nil {
			t.Fatal(err)
		}
		res, err := lib.store.SyncDerived(ctx(t), id, lib.deriver(), true)
		if err != nil || res.Created != 1 {
			t.Fatalf("SyncDerived = %+v, %v", res, err)
		}
	})
}

// TestSoftDeletedMemoryKeepsCopies: garbage collection only removes copies
// that no memory image references.
func TestSoftDeletedMemoryKeepsCopies(t *testing.T) {
	lib := newTestLibrary(t)
	lib.addPhoto(t, "f1", "a.jpg", 10, 10)
	m, _ := lib.store.Create(ctx(t), memories.CreateParams{Title: "t"})
	if _, err := lib.store.SaveDocument(ctx(t), m.ID, nil,
		[]*memories.Block{editedImageBlock("f1", memories.Edits{Filter: memories.FilterSoft})}); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.store.SyncDerived(ctx(t), m.ID, lib.deriver(), true); err != nil {
		t.Fatal(err)
	}
	if err := lib.store.Delete(ctx(t), m.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := lib.store.CollectDerived(ctx(t), lib.deriver()); err != nil || n != 0 {
		t.Fatalf("collected %d (%v) copies of a soft-deleted memory", n, err)
	}
	if len(derivedFiles(t, lib)) != 1 {
		t.Errorf("copy removed")
	}
}
