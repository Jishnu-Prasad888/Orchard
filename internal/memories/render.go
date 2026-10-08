package memories

import (
	"fmt"
	"image"
	"image/draw"
	"math"
	"os"

	"github.com/Jishnu-Prasad888/Cairn/internal/metadata"
	"github.com/Jishnu-Prasad888/Cairn/internal/safeimage"
)

// RenderEdited decodes the original at srcPath (opened read-only; the file
// is never written) and applies edits in the canonical order documented on
// Edits. The result is a new in-memory image.
func RenderEdited(srcPath string, e Edits) (*image.NRGBA, error) {
	f, err := os.Open(srcPath)
	if err != nil {
		return nil, fmt.Errorf("open original: %w", err)
	}
	defer func() { _ = f.Close() }()
	decoded, _, err := safeimage.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("decode original: %w", err)
	}

	img := toNRGBA(decoded)
	// Browsers display the original with its EXIF orientation applied, and
	// the crop rectangle was drawn on that view, so apply it first.
	if info, err := metadata.ExtractFromFile(srcPath); err == nil {
		img = applyOrientation(img, info.Orientation)
	}
	switch e.Rotation {
	case 90:
		img = transform(img, rot90)
	case 180:
		img = transform(img, rot180)
	case 270:
		img = transform(img, rot270)
	}
	if c := e.Crop; c != nil {
		img = cropNormalized(img, *c)
	}
	applyFilterOps(img, e.FilterOps())
	return img, nil
}

func toNRGBA(src image.Image) *image.NRGBA {
	if n, ok := src.(*image.NRGBA); ok && n.Rect.Min == (image.Point{}) {
		return n
	}
	b := src.Bounds()
	dst := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Src)
	return dst
}

type orientOp int

const (
	rot90 orientOp = iota // clockwise
	rot180
	rot270 // clockwise (90° counter-clockwise)
	flipH
	flipV
	transpose  // mirror across the main diagonal
	transverse // mirror across the anti-diagonal
)

// applyOrientation maps an EXIF orientation tag (1–8) to the transform that
// shows the image upright.
func applyOrientation(img *image.NRGBA, o int) *image.NRGBA {
	switch o {
	case 2:
		return transform(img, flipH)
	case 3:
		return transform(img, rot180)
	case 4:
		return transform(img, flipV)
	case 5:
		return transform(img, transpose)
	case 6:
		return transform(img, rot90)
	case 7:
		return transform(img, transverse)
	case 8:
		return transform(img, rot270)
	}
	return img
}

// transform returns a new image with op applied, by mapping every
// destination pixel back to its source pixel.
func transform(src *image.NRGBA, op orientOp) *image.NRGBA {
	w, h := src.Rect.Dx(), src.Rect.Dy()
	dw, dh := w, h
	if op == rot90 || op == rot270 || op == transpose || op == transverse {
		dw, dh = h, w
	}
	dst := image.NewNRGBA(image.Rect(0, 0, dw, dh))
	for dy := 0; dy < dh; dy++ {
		for dx := 0; dx < dw; dx++ {
			var sx, sy int
			switch op {
			case rot90:
				sx, sy = dy, h-1-dx
			case rot180:
				sx, sy = w-1-dx, h-1-dy
			case rot270:
				sx, sy = w-1-dy, dx
			case flipH:
				sx, sy = w-1-dx, dy
			case flipV:
				sx, sy = dx, h-1-dy
			case transpose:
				sx, sy = dy, dx
			case transverse:
				sx, sy = w-1-dy, h-1-dx
			}
			si := src.PixOffset(sx, sy)
			di := dst.PixOffset(dx, dy)
			copy(dst.Pix[di:di+4], src.Pix[si:si+4])
		}
	}
	return dst
}

func cropNormalized(src *image.NRGBA, c Crop) *image.NRGBA {
	w, h := float64(src.Rect.Dx()), float64(src.Rect.Dy())
	x0 := int(math.Round(c.X * w))
	y0 := int(math.Round(c.Y * h))
	x1 := int(math.Round((c.X + c.Width) * w))
	y1 := int(math.Round((c.Y + c.Height) * h))
	r := image.Rect(x0, y0, max(x1, x0+1), max(y1, y0+1)).Intersect(src.Rect)
	dst := image.NewNRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	draw.Draw(dst, dst.Bounds(), src, r.Min, draw.Src)
	return dst
}

// colorMatrix is a 3×3 RGB transform as defined for the CSS saturate,
// grayscale, sepia and hue-rotate filter functions.
type colorMatrix [3][3]float64

func opMatrix(op FilterOp) (colorMatrix, bool) {
	a := op.Amount
	switch op.Kind {
	case "saturate":
		return colorMatrix{
			{0.213 + 0.787*a, 0.715 - 0.715*a, 0.072 - 0.072*a},
			{0.213 - 0.213*a, 0.715 + 0.285*a, 0.072 - 0.072*a},
			{0.213 - 0.213*a, 0.715 - 0.715*a, 0.072 + 0.928*a},
		}, true
	case "grayscale":
		g := 1 - math.Min(1, a)
		return colorMatrix{
			{0.2126 + 0.7874*g, 0.7152 - 0.7152*g, 0.0722 - 0.0722*g},
			{0.2126 - 0.2126*g, 0.7152 + 0.2848*g, 0.0722 - 0.0722*g},
			{0.2126 - 0.2126*g, 0.7152 - 0.7152*g, 0.0722 + 0.9278*g},
		}, true
	case "sepia":
		s := 1 - math.Min(1, a)
		return colorMatrix{
			{0.393 + 0.607*s, 0.769 - 0.769*s, 0.189 - 0.189*s},
			{0.349 - 0.349*s, 0.686 + 0.314*s, 0.168 - 0.168*s},
			{0.272 - 0.272*s, 0.534 - 0.534*s, 0.131 + 0.869*s},
		}, true
	case "hue-rotate":
		rad := a * math.Pi / 180
		cos, sin := math.Cos(rad), math.Sin(rad)
		return colorMatrix{
			{0.213 + cos*0.787 - sin*0.213, 0.715 - cos*0.715 - sin*0.715, 0.072 - cos*0.072 + sin*0.928},
			{0.213 - cos*0.213 + sin*0.143, 0.715 + cos*0.285 + sin*0.140, 0.072 - cos*0.072 - sin*0.283},
			{0.213 - cos*0.213 - sin*0.787, 0.715 - cos*0.715 + sin*0.715, 0.072 + cos*0.928 + sin*0.072},
		}, true
	}
	return colorMatrix{}, false
}

// applyFilterOps runs the CSS-equivalent primitives over every pixel in
// place, clamping after each primitive as browsers do.
func applyFilterOps(img *image.NRGBA, ops []FilterOp) {
	if len(ops) == 0 {
		return
	}
	type step struct {
		kind   string
		amount float64
		m      colorMatrix
		matrix bool
	}
	steps := make([]step, 0, len(ops))
	for _, op := range ops {
		m, isMatrix := opMatrix(op)
		steps = append(steps, step{op.Kind, op.Amount, m, isMatrix})
	}
	pix := img.Pix
	for i := 0; i+3 < len(pix); i += 4 {
		r, g, b := float64(pix[i])/255, float64(pix[i+1])/255, float64(pix[i+2])/255
		for _, s := range steps {
			switch {
			case s.matrix:
				r, g, b = s.m[0][0]*r+s.m[0][1]*g+s.m[0][2]*b,
					s.m[1][0]*r+s.m[1][1]*g+s.m[1][2]*b,
					s.m[2][0]*r+s.m[2][1]*g+s.m[2][2]*b
			case s.kind == "brightness":
				r, g, b = r*s.amount, g*s.amount, b*s.amount
			case s.kind == "contrast":
				r = (r-0.5)*s.amount + 0.5
				g = (g-0.5)*s.amount + 0.5
				b = (b-0.5)*s.amount + 0.5
			}
			r, g, b = clamp01(r), clamp01(g), clamp01(b)
		}
		pix[i] = uint8(math.Round(r * 255))
		pix[i+1] = uint8(math.Round(g * 255))
		pix[i+2] = uint8(math.Round(b * 255))
	}
}
