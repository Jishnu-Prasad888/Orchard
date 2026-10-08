package safeimage

import (
	"bytes"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// patchPNG returns a valid 1×1 PNG whose IHDR width/height claims the given
// dimensions, so DecodeConfig reports them without allocating pixel data.
func patchPNG(t *testing.T, w, h uint32) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode base png: %v", err)
	}
	raw := buf.Bytes()
	// IHDR payload starts after 8-byte signature + 4-byte length + 4-byte
	// "IHDR" chunk type => offset 16. Width is bytes 16-19, height 20-23.
	copy(raw[16:20], []byte{byte(w >> 24), byte(w >> 16), byte(w >> 8), byte(w)})
	copy(raw[20:24], []byte{byte(h >> 24), byte(h >> 16), byte(h >> 8), byte(h)})
	// Recompute the chunk CRC-32 over type + data (bytes 12..29).
	crc := crc32.NewIEEE()
	crc.Write(raw[12:29])
	copy(raw[29:33], crc.Sum(nil))
	return raw
}

func TestDecodeRejectsOversized(t *testing.T) {
	cases := []struct {
		name string
		w, h uint32
	}{
		{"huge width", 30_001, 10},
		{"huge height", 10, 30_001},
		{"huge pixels", 20_000, 3_000},
		{"modest but over pixel cap", 40_000, 40_000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := Decode(bytes.NewReader(patchPNG(t, tc.w, tc.h)))
			if !errors.Is(err, ErrTooLarge) {
				t.Fatalf("Decode error = %v, want ErrTooLarge", err)
			}
		})
	}
}

func TestDecodeRejectsOversizedConfig(t *testing.T) {
	_, _, err := DecodeConfig(bytes.NewReader(patchPNG(t, 50_000, 50_000)))
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("DecodeConfig error = %v, want ErrTooLarge", err)
	}
}

func TestDecodeAcceptsSmall(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 64, 48))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	decoded, format, err := Decode(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("Decode small image: %v", err)
	}
	if format != "png" {
		t.Errorf("format = %q, want png", format)
	}
	b := decoded.Bounds()
	if b.Dx() != 64 || b.Dy() != 48 {
		t.Errorf("bounds = %v, want 64x48", b)
	}
}
