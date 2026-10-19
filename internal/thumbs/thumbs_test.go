package thumbs

import (
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/Jishnu-Prasad888/Orchard/internal/jobs"
	"github.com/Jishnu-Prasad888/Orchard/internal/librarydb"
)

// halves is a w×h image, red on the left and blue on the right.
func halves(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			c := color.RGBA{0xff, 0, 0, 0xff}
			if x >= w/2 {
				c = color.RGBA{0, 0, 0xff, 0xff}
			}
			img.Set(x, y, c)
		}
	}
	return img
}

// withOrientation encodes img as a JPEG carrying an EXIF Orientation tag.
func withOrientation(t *testing.T, img image.Image, orientation uint16) []byte {
	t.Helper()
	var enc bytes.Buffer
	if err := jpeg.Encode(&enc, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	tiff := []byte("MM\x00\x2a\x00\x00\x00\x08")  // big-endian, IFD0 at 8
	tiff = binary.BigEndian.AppendUint16(tiff, 1) // one entry
	tiff = binary.BigEndian.AppendUint16(tiff, 0x0112)
	tiff = binary.BigEndian.AppendUint16(tiff, 3) // SHORT
	tiff = binary.BigEndian.AppendUint32(tiff, 1)
	tiff = binary.BigEndian.AppendUint16(tiff, orientation)
	tiff = append(tiff, 0, 0, 0, 0, 0, 0) // value padding + next IFD
	seg := append([]byte("Exif\x00\x00"), tiff...)
	app1 := []byte{0xFF, 0xE1}
	app1 = binary.BigEndian.AppendUint16(app1, uint16(len(seg)+2))
	app1 = append(app1, seg...)
	raw := enc.Bytes()
	return append(append(append([]byte{}, raw[:2]...), app1...), raw[2:]...)
}

func renderBytes(t *testing.T, data []byte) image.Image {
	t.Helper()
	path := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := Render(path)
	if err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func isRed(c color.Color) bool {
	r, g, b, _ := c.RGBA()
	return r > 0xc000 && g < 0x4000 && b < 0x4000
}

func TestRenderShrinksToTheShortSide(t *testing.T) {
	var enc bytes.Buffer
	_ = jpeg.Encode(&enc, halves(2000, 1000), nil)
	img := renderBytes(t, enc.Bytes())
	if got := img.Bounds().Size(); got != (image.Point{X: 1024, Y: 512}) {
		t.Fatalf("size = %v, want 1024x512", got)
	}
	if !isRed(img.At(10, 256)) || isRed(img.At(1000, 256)) {
		t.Fatal("halves lost in the downscale")
	}
}

func TestRenderTurnsPhonePhotosUpright(t *testing.T) {
	// Stored landscape, red on the left; orientation 6 means "rotate 90°
	// clockwise to view", which puts the red half on top.
	img := renderBytes(t, withOrientation(t, halves(400, 200), 6))
	if got := img.Bounds().Size(); got != (image.Point{X: 200, Y: 400}) {
		t.Fatalf("size = %v, want 200x400", got)
	}
	if !isRed(img.At(100, 20)) || isRed(img.At(100, 380)) {
		t.Fatal("orientation 6 not applied: red half should be on top")
	}
}

func TestRenderRefusesWhatItCannotRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "photo.heic")
	_ = os.WriteFile(path, []byte("not an image"), 0o644)
	if _, err := Render(path); err == nil {
		t.Fatal("rendered garbage")
	}
}

func TestProcessorRecordsAndBackfills(t *testing.T) {
	root := t.TempDir()
	orchardDir := filepath.Join(root, ".orchard")
	if err := os.MkdirAll(orchardDir, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := librarydb.Open(orchardDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	var enc bytes.Buffer
	_ = jpeg.Encode(&enc, halves(64, 32), nil)
	_ = os.WriteFile(filepath.Join(root, "a.jpg"), enc.Bytes(), 0o644)
	_ = os.WriteFile(filepath.Join(root, "b.jpg"), []byte("broken"), 0o644)
	for _, id := range []string{"a", "b"} {
		if _, err := db.Exec(`INSERT INTO indexed_files
			(id, rel_path, size_bytes, mod_time, media_type, status, first_seen_at, last_seen_at, indexed_at)
			VALUES (?, ?, 1, 't1', 'photo', 'present', 't', 't', 't')`, id, id+".jpg"); err != nil {
			t.Fatal(err)
		}
	}
	p := NewProcessor(slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := context.Background()

	pending, err := p.Pending(ctx, db)
	if err != nil || len(pending) != 2 {
		t.Fatalf("pending = %v, %v", pending, err)
	}
	for id, rel := range pending {
		job := &jobs.Job{Payload: map[string]any{
			"file_id": id, "rel_path": rel, "root": root, "orchard_dir": orchardDir,
		}}
		if err := p.Handle(ctx, db, job); err != nil {
			t.Fatal(err)
		}
	}
	// Both are recorded (b as unsupported), so neither is pending again.
	if pending, _ := p.Pending(ctx, db); len(pending) != 0 {
		t.Fatalf("still pending: %v", pending)
	}
	versions, err := Versions(ctx, db, []string{"a", "b"})
	if err != nil || len(versions) != 1 || versions["a"] == "" {
		t.Fatalf("versions = %v, %v; want only a", versions, err)
	}
	if _, err := os.Stat(filepath.Join(orchardDir, Dir, "a.jpg")); err != nil {
		t.Fatalf("thumbnail file: %v", err)
	}

	// Editing the photo makes it pending again.
	_, _ = db.Exec(`UPDATE indexed_files SET mod_time = 't2' WHERE id = 'a'`)
	if pending, _ := p.Pending(ctx, db); pending["a"] != "a.jpg" {
		t.Fatalf("edited photo not pending: %v", pending)
	}
}
