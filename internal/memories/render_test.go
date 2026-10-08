package memories_test

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"

	"github.com/Jishnu-Prasad888/Cairn/internal/memories"
)

func bytesReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }

// quadrantJPEG writes a 40x20 image whose left half is red and right half is
// blue, so rotation and crop direction are observable.
func quadrantJPEG(t *testing.T) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 40, 20))
	for y := 0; y < 20; y++ {
		for x := 0; x < 40; x++ {
			c := color.RGBA{220, 20, 20, 255}
			if x >= 20 {
				c = color.RGBA{20, 20, 220, 255}
			}
			img.Set(x, y, c)
		}
	}
	path := filepath.Join(t.TempDir(), "q.jpg")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: 100}); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	return path
}

func isRed(c color.Color) bool {
	r, g, b, _ := c.RGBA()
	return r>>8 > 150 && g>>8 < 90 && b>>8 < 90
}

func isBlue(c color.Color) bool {
	r, g, b, _ := c.RGBA()
	return b>>8 > 150 && r>>8 < 90 && g>>8 < 90
}

func TestRenderRotationAndCrop(t *testing.T) {
	path := quadrantJPEG(t)

	// 90° clockwise: the left (red) half ends up on top.
	out, err := memories.RenderEdited(path, memories.Edits{Rotation: 90})
	if err != nil {
		t.Fatal(err)
	}
	if b := out.Bounds(); b.Dx() != 20 || b.Dy() != 40 {
		t.Fatalf("rotated size = %v", b)
	}
	if !isRed(out.At(10, 5)) || !isBlue(out.At(10, 35)) {
		t.Errorf("rotate 90: top=%v bottom=%v", out.At(10, 5), out.At(10, 35))
	}

	// 270°: red at the bottom.
	out, _ = memories.RenderEdited(path, memories.Edits{Rotation: 270})
	if !isBlue(out.At(10, 5)) || !isRed(out.At(10, 35)) {
		t.Errorf("rotate 270: top=%v bottom=%v", out.At(10, 5), out.At(10, 35))
	}

	// Crop is applied in the rotated frame: keep the right half of the
	// unrotated image.
	out, _ = memories.RenderEdited(path, memories.Edits{Crop: &memories.Crop{X: 0.5, Y: 0, Width: 0.5, Height: 1}})
	if b := out.Bounds(); b.Dx() != 20 || b.Dy() != 20 {
		t.Fatalf("crop size = %v", b)
	}
	if !isBlue(out.At(10, 10)) {
		t.Errorf("crop kept the wrong half: %v", out.At(10, 10))
	}
}

func TestRenderFilters(t *testing.T) {
	path := quadrantJPEG(t)
	out, err := memories.RenderEdited(path, memories.Edits{Filter: memories.FilterBW})
	if err != nil {
		t.Fatal(err)
	}
	r, g, b, _ := out.At(5, 5).RGBA()
	if r != g || g != b {
		t.Errorf("b&w pixel not gray: %d %d %d", r>>8, g>>8, b>>8)
	}

	bright, _ := memories.RenderEdited(path, memories.Edits{Adjustments: memories.Adjustments{Brightness: 100}})
	dark, _ := memories.RenderEdited(path, memories.Edits{Adjustments: memories.Adjustments{Brightness: -100}})
	br, _, _, _ := bright.At(5, 5).RGBA()
	dr, _, _, _ := dark.At(5, 5).RGBA()
	if br <= dr {
		t.Errorf("brightness has no effect: %d <= %d", br>>8, dr>>8)
	}
}

func TestEditsDefaultsAndSignature(t *testing.T) {
	var e memories.Edits
	if !e.IsDefault() {
		t.Error("zero edits should be default")
	}
	full := memories.Edits{Crop: &memories.Crop{X: 0, Y: 0, Width: 1, Height: 1}, Filter: memories.FilterOriginal}
	if err := full.Validate(); err != nil {
		t.Fatal(err)
	}
	if full.Crop != nil || !full.IsDefault() {
		t.Error("a full-frame crop should normalize to no crop")
	}
	a := memories.Edits{Rotation: 90}
	b := memories.Edits{Rotation: 180}
	if a.Signature("f") == b.Signature("f") || a.Signature("f") == a.Signature("g") {
		t.Error("signature ignores rotation or source")
	}
	if len(a.FilterOps()) != 0 || len(memories.Edits{Filter: memories.FilterWarm}.FilterOps()) == 0 {
		t.Error("filter ops wrong")
	}
}
