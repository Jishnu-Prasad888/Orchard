// Package thumbs makes the small images photo grids show.
//
// A grid used to load every photo's original — several megabytes each from a
// phone — to draw a tile a couple of hundred pixels wide. A background job now
// renders each photo once into a JPEG whose short side is ShortSide pixels,
// stored under the library's .orchard/thumbs/, and listings point tiles at it.
// Until a photo's thumbnail exists the tile keeps loading the original, so
// nothing is ever worse than before.
//
// Rendering uses only the standard library: a box (area-averaging) filter,
// which is what downscaling by a large factor wants — every source pixel
// counts once, so there is no aliasing — and costs one pass over the source.
package thumbs

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"io"
	"os"

	_ "image/gif" // register decoders
	_ "image/png"
)

const (
	// ShortSide is the thumbnail's shorter edge. Grid tiles are square crops
	// at most ~260 CSS px, so this stays sharp on a 2-3x screen.
	ShortSide = 512
	// Quality is the JPEG quality; tiles are small, so artefacts don't show.
	Quality = 80

	// maxJPEGPixels and maxOtherPixels bound decoding memory on a Pi: a
	// JPEG decodes to ~1.5 bytes a pixel, anything else to 4.
	maxJPEGPixels  = 50_000_000
	maxOtherPixels = 20_000_000
)

// ErrUnsupported means the file is not an image this package can shrink
// (an unknown format, or too large to decode safely). The grid then keeps
// using the original.
var ErrUnsupported = errors.New("unsupported image")

// Render returns a JPEG thumbnail of the image at path, upright per its EXIF
// orientation. Images already small enough are re-encoded at their own size.
func Render(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	cfg, format, err := image.DecodeConfig(f)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnsupported, err)
	}
	limit := maxOtherPixels
	if format == "jpeg" {
		limit = maxJPEGPixels
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > limit {
		return nil, fmt.Errorf("%w: %dx%d %s", ErrUnsupported, cfg.Width, cfg.Height, format)
	}

	orientation := 1
	if format == "jpeg" {
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return nil, err
		}
		orientation = exifOrientation(f)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	src, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnsupported, err)
	}

	b := src.Bounds()
	dw, dh := fitShortSide(b.Dx(), b.Dy(), ShortSide)
	img := orient(downscale(src, dw, dh), orientation)

	var out bytes.Buffer
	if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: Quality}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// fitShortSide scales w×h so its shorter side is at most short, never up.
func fitShortSide(w, h, short int) (int, int) {
	s := min(w, h)
	if s <= short {
		return w, h
	}
	return max(1, (w*short+s/2)/s), max(1, (h*short+s/2)/s)
}

// downscale box-filters src to dw×dh. JPEG's YCbCr images take a fast path
// reading the planes directly; anything else is first drawn onto white (so
// transparency doesn't turn black in a JPEG).
func downscale(src image.Image, dw, dh int) *image.RGBA {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))

	var pixel func(x, y int) (r, g, b uint32)
	switch s := src.(type) {
	case *image.YCbCr:
		pixel = func(x, y int) (uint32, uint32, uint32) {
			px, py := b.Min.X+x, b.Min.Y+y
			yi, ci := s.YOffset(px, py), s.COffset(px, py)
			r, g, bb := color.YCbCrToRGB(s.Y[yi], s.Cb[ci], s.Cr[ci])
			return uint32(r), uint32(g), uint32(bb)
		}
	default:
		rgba, ok := src.(*image.RGBA)
		if !ok || !opaque(src) {
			rgba = image.NewRGBA(image.Rect(0, 0, sw, sh))
			draw.Draw(rgba, rgba.Bounds(), image.White, image.Point{}, draw.Src)
			draw.Draw(rgba, rgba.Bounds(), src, b.Min, draw.Over)
		}
		rb := rgba.Bounds()
		pixel = func(x, y int) (uint32, uint32, uint32) {
			i := rgba.PixOffset(rb.Min.X+x, rb.Min.Y+y)
			return uint32(rgba.Pix[i]), uint32(rgba.Pix[i+1]), uint32(rgba.Pix[i+2])
		}
	}

	// Which destination column each source column lands in.
	col := make([]int, sw)
	for x := range sw {
		col[x] = x * dw / sw
	}
	sumR := make([]uint32, dw)
	sumG := make([]uint32, dw)
	sumB := make([]uint32, dw)
	count := make([]uint32, dw)
	flush := func(dy int) {
		for dx := range dw {
			n := max(count[dx], 1)
			i := dst.PixOffset(dx, dy)
			dst.Pix[i] = uint8((sumR[dx] + n/2) / n)
			dst.Pix[i+1] = uint8((sumG[dx] + n/2) / n)
			dst.Pix[i+2] = uint8((sumB[dx] + n/2) / n)
			dst.Pix[i+3] = 0xff
			sumR[dx], sumG[dx], sumB[dx], count[dx] = 0, 0, 0, 0
		}
	}
	row := 0
	for y := range sh {
		if dy := y * dh / sh; dy != row {
			flush(row)
			row = dy
		}
		for x := range sw {
			r, g, bl := pixel(x, y)
			dx := col[x]
			sumR[dx] += r
			sumG[dx] += g
			sumB[dx] += bl
			count[dx]++
		}
	}
	flush(row)
	return dst
}

func opaque(img image.Image) bool {
	o, ok := img.(interface{ Opaque() bool })
	return ok && o.Opaque()
}

// orient applies an EXIF orientation (1-8) to img so it displays upright:
// the thumbnail carries no EXIF, so the browser won't rotate it.
func orient(img *image.RGBA, orientation int) *image.RGBA {
	if orientation < 2 || orientation > 8 {
		return img
	}
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	ow, oh := w, h
	if orientation >= 5 { // these swap width and height
		ow, oh = h, w
	}
	out := image.NewRGBA(image.Rect(0, 0, ow, oh))
	for y := range h {
		for x := range w {
			var nx, ny int
			switch orientation {
			case 2: // mirrored
				nx, ny = w-1-x, y
			case 3: // upside down
				nx, ny = w-1-x, h-1-y
			case 4: // mirrored, upside down
				nx, ny = x, h-1-y
			case 5: // mirrored, rotated 90° counter-clockwise
				nx, ny = y, x
			case 6: // rotated 90° clockwise
				nx, ny = h-1-y, x
			case 7: // mirrored, rotated 90° clockwise
				nx, ny = h-1-y, w-1-x
			case 8: // rotated 90° counter-clockwise
				nx, ny = y, w-1-x
			}
			si, di := img.PixOffset(x, y), out.PixOffset(nx, ny)
			copy(out.Pix[di:di+4], img.Pix[si:si+4])
		}
	}
	return out
}
