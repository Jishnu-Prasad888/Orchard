package ml

import (
	"bytes"
	_ "embed"
	"fmt"
	"image"
	"image/jpeg"
	"math"
	"sync"

	pigo "github.com/esimov/pigo/core"
	"golang.org/x/image/draw"
)

//go:embed cascade/facefinder
var faceCascade []byte

// FaceBox is one detected face within an image, expressed in source-image
// pixels. Width and Height describe a square window (the LBP cascade reports
// a square detection).
type FaceBox struct {
	X          int
	Y          int
	Width      int
	Height     int
	Confidence float64 // normalized detector score, 0..1

	// Landmarks holds the 5-point facial landmarks returned by SCRFD:
	// [0]=left_eye, [1]=right_eye, [2]=nose, [3]=left_mouth, [4]=right_mouth,
	// each as (x, y) in source-image pixels. Zero when the detector does not
	// provide landmarks (e.g. the legacy pigo provider).
	Landmarks [5][2]float32
}

// FaceProvider computes face detections and per-face descriptors for images.
// It is the Phase 15 extension of the Phase 11 provider seam: a provider
// identifies its algorithm via Name and Version, and a future learned
// embedder can replace the default values without changing callers by bumping
// Version and re-running a pass.
type FaceProvider interface {
	// Name is a stable identifier for the provider, e.g. "pigo_appearance".
	Name() string
	// Version identifies the detection+embedding algorithm. Changing either
	// algorithm bumps this so stored rows are never misread.
	Version() int
	// Detect returns the faces found in a decoded image.
	Detect(img image.Image) ([]FaceBox, error)
	// Embed derives a fixed-size appearance descriptor for a face crop taken
	// from img at box. The crop is supplied by the caller (the pipeline keeps
	// the source image in memory for the embed step), so Embed only sees the
	// localized patch plus the source bounds for correct cropping.
	Embed(img image.Image, box FaceBox) ([]float32, error)
	// Describe returns a short human-readable summary of the algorithm.
	Describe() string
}

// PigoFaceProvider is the built-in face provider: pigo LBP-cascade frontal
// face detection with a dependency-free appearance descriptor (16x16
// z-normalized grayscale) as the embedding. It is honest about what it is:
// it groups frontal faces that look alike and is not a biometric identity
// matcher across pose, lighting, or aging.
type PigoFaceProvider struct {
	// MinSize is the smallest detection window, in pixels, expressed as the
	// cascade's scale. Larger values scan faster.
	MinSize int
	// MinConfidence is the floor for a detection's normalized score (0..1).
	// pigo reports scores around 5..30 for real frontal faces, so 0.05 is a
	// sensible default (Q == 5) and 0.01 accepts much noisier detections.
	MinConfidence float64
	// maxSize, when non-zero, caps the largest detection window. Zero
	// derives a sensible maximum from the image dimensions.
	maxSize int
}

// NewPigoFaceProvider returns a PigoFaceProvider with safe defaults. minSize
// defaults to 60; minConfidence defaults to 0.05 when zero; a negative value
// for minSize restores the default.
func NewPigoFaceProvider(minSize int, minConfidence float64) *PigoFaceProvider {
	if minSize <= 0 {
		minSize = 60
	}
	if minConfidence <= 0 {
		minConfidence = 0.05
	}
	return &PigoFaceProvider{MinSize: minSize, MinConfidence: minConfidence}
}

// Name returns the provider identifier.
func (PigoFaceProvider) Name() string { return "pigo_appearance" }

// Version is the detection+embedding algorithm version. Bump when either
// algorithm changes so stored rows are invalidated cleanly.
func (PigoFaceProvider) Version() int { return 1 }

// Describe returns a short human-readable summary.
func (PigoFaceProvider) Describe() string {
	return "pigo LBP frontal-face detection + 16x16 appearance descriptor (local, dependency-free)"
}

// Detector runs the unpacked pigo cascade. It is initialized once and reused
// for every image; pigo's Pigo type is stateless after Unpack.
var (
	detectorOnce sync.Once
	detector     *pigo.Pigo
	detectorErr  error
)

// unpackCascade lazily installs the embedded facefinder cascade.
func unpackCascade() (*pigo.Pigo, error) {
	detectorOnce.Do(func() {
		detector, detectorErr = pigo.NewPigo().Unpack(faceCascade)
	})
	return detector, detectorErr
}

// Detect runs the LBP cascade over the decoded image and returns the faces
// found. It is moderately expensive (a bounded window scan) and is expected to
// run inside the bounded-worker ML passes.
func (p *PigoFaceProvider) Detect(img image.Image) ([]FaceBox, error) {
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w < p.MinSize+16 || h < p.MinSize+16 {
		return nil, nil
	}
	classifier, err := unpackCascade()
	if err != nil {
		return nil, fmt.Errorf("face cascade: %w", err)
	}

	maxSize := p.maxSize
	if maxSize <= 0 || maxSize > w || maxSize > h {
		maxSize = w
		if h < maxSize {
			maxSize = h
		}
	}
	if maxSize < p.MinSize {
		maxSize = p.MinSize
	}

	dets := classifier.RunCascade(pigo.CascadeParams{
		MinSize:     p.MinSize,
		MaxSize:     maxSize,
		ShiftFactor: 0.15,
		ScaleFactor: 1.1,
		ImageParams: pigo.ImageParams{
			Pixels: imageToGrayPixels(img, bounds),
			Rows:   h,
			Cols:   w,
			Dim:    w,
		},
	}, 0.0)
	// IoU clustering merges overlapping detections; a small threshold keeps
	// only the strongest detection per face.
	dets = classifier.ClusterDetections(dets, 0.2)

	faces := make([]FaceBox, 0, len(dets))
	for _, d := range dets {
		conf := float64(d.Q) / 100.0
		if conf > 1.0 {
			conf = 1.0
		}
		if conf < p.MinConfidence {
			continue
		}
		half := d.Scale / 2
		x := d.Col - half
		y := d.Row - half
		faces = append(faces, FaceBox{
			X:          x,
			Y:          y,
			Width:      d.Scale,
			Height:     d.Scale,
			Confidence: conf,
		})
	}
	return faces, nil
}

// Embed derives the appearance descriptor for a face: crop the box, resize to
// 16x16 grayscale, and z-normalize the 256 samples. The result is a
// zero-mean, unit-scale vector compared by cosine similarity during
// clustering.
func (PigoFaceProvider) Embed(img image.Image, box FaceBox) ([]float32, error) {
	crop, err := faceCrop(img, box, 48)
	if err != nil {
		return nil, err
	}
	gray, ok := crop.(*image.Gray)
	if !ok {
		b := crop.Bounds()
		g := image.NewGray(b)
		draw.Draw(g, b, crop, b.Min, draw.Src)
		gray = g
	}
	return appearanceDescriptor(gray), nil
}

// FaceJPEG renders a serving-size JPEG of the face at box within the decoded
// source image. It is derived output: nothing is written to disk. maxDim
// clamps the longest side of the crop (when <= 0, 384 is used).
func FaceJPEG(img image.Image, box FaceBox, maxDim int) ([]byte, error) {
	if maxDim <= 0 {
		maxDim = 384
	}
	crop, err := faceCrop(img, box, maxDim)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, crop, &jpeg.Options{Quality: 88}); err != nil {
		return nil, fmt.Errorf("encode face crop: %w", err)
	}
	return buf.Bytes(), nil
}

// faceCrop extracts a square-ish image.Image containing the face at box,
// inflated by 10% so the descriptor sees hair/chin context, then scaled so
// its longest side is size pixels. Very small or out-of-bounds crops are
// flattened to the hole in the image so the pipeline never fails a whole file
// on one bad box.
func faceCrop(img image.Image, box FaceBox, size int) (image.Image, error) {
	b := img.Bounds()
	if box.Width <= 0 || box.Height <= 0 {
		return nil, fmt.Errorf("degenerate face box %+v", box)
	}
	in := 10
	x0 := box.X - box.Width*in/100
	y0 := box.Y - box.Height*in/100
	x1 := box.X + box.Width + box.Width*in/100
	y1 := box.Y + box.Height + box.Height*in/100
	if x0 < b.Min.X {
		x0 = b.Min.X
	}
	if y0 < b.Min.Y {
		y0 = b.Min.Y
	}
	if x1 > b.Max.X {
		x1 = b.Max.X
	}
	if y1 > b.Max.Y {
		y1 = b.Max.Y
	}
	if x1-x0 < 8 || y1-y0 < 8 {
		return nil, fmt.Errorf("face crop too small (%d x %d)", x1-x0, y1-y0)
	}
	w, h := x1-x0, y1-y0
	scale := float64(size) / float64(w)
	if h > w {
		scale = float64(size) / float64(h)
	}
	if scale > 1 {
		scale = 1 // never upscale crops beyond the source resolution
	}
	dw := int(float64(w) * scale)
	dh := int(float64(h) * scale)
	if dw < 1 {
		dw = 1
	}
	if dh < 1 {
		dh = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, image.Rect(x0, y0, x1, y1), draw.Over, nil)
	return dst, nil
}

// appearanceDescriptor downsamples a grayscale crop to 16x16 (non-overlapping
// 3x3 blocks of a 48x48 input) and z-normalizes the 256 samples.
func appearanceDescriptor(g *image.Gray) []float32 {
	out := make([]float32, 0, 16*16)
	var mean float64
	for by := 0; by < 48; by += 3 {
		for bx := 0; bx < 48; bx += 3 {
			var s int
			for dy := 0; dy < 3; dy++ {
				for dx := 0; dx < 3; dx++ {
					idx := (by+dy)*g.Stride + (bx + dx)
					if idx < len(g.Pix) {
						s += int(g.Pix[idx])
					}
				}
			}
			f := float64(s) / 9.0
			mean += f
			out = append(out, float32(f))
		}
	}
	mean /= float64(len(out))
	var stdev float64
	for _, v := range out {
		d := float64(v) - mean
		stdev += d * d
	}
	stdev = math.Sqrt(stdev / float64(len(out)))
	if stdev < 1e-6 {
		// Flat crop: emit the zero vector; clustering treats it as unmatched.
		for i := range out {
			out[i] = 0
		}
		return out
	}
	for i, v := range out {
		out[i] = float32((float64(v) - mean) / stdev)
	}
	return out
}

// imageToGrayPixels renders an image's bounds as a row-major 1-byte-per-pixel
// luminance buffer, which is what the pigo cascade consumes.
func imageToGrayPixels(img image.Image, b image.Rectangle) []uint8 {
	g := image.NewGray(b)
	draw.Draw(g, b, img, b.Min, draw.Src)
	// image.NewGray uses the same luminance weights as image/color.GrayModel;
	// return its pixel buffer directly and let the cascade read it.
	if g.Rect == b {
		return g.Pix
	}
	out := make([]uint8, b.Dx()*b.Dy())
	copy(out, g.Pix)
	return out
}

// DescriptorCosine returns the cosine similarity of two equal-length
// descriptors, in [0, 1] once both are normalized (the appearance descriptor
// is unit- or zero-scale).
func DescriptorCosine(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot float64
	var na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
