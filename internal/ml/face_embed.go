package ml

import (
	"fmt"
	"image"
	"math"

	"github.com/Jishnu-Prasad888/Cairn/internal/ml/onnx"
)

// Reference 5-point landmarks for the InsightFace 112×112 aligned face crop
// that ArcFace-family networks are trained on (arcface_112v1 standard).
// Order: left-eye, right-eye, nose-tip, left-mouth, right-mouth.
const (
	alignSize = 112
)

var arcfaceRef = [5][2]float64{
	{38.2946, 51.6963}, // left eye
	{73.5318, 51.5014}, // right eye
	{56.0252, 71.7366}, // nose tip
	{41.5493, 92.3655}, // left mouth corner
	{70.7299, 92.2041}, // right mouth corner
}

const embedDimension = 512

// SCRFDEmbeddingFaceProvider runs SCRFD for detection (with 5-point landmarks)
// and ArcFace ResNet50 (w600k_r50) for recognition. The two models are kept
// in separate files so each can be upgraded independently. Detection and
// embedding are combined in this single provider so the FaceProvider interface
// is satisfied by one object.
type SCRFDEmbeddingFaceProvider struct {
	detector *SCRFDDetector
	model    *onnx.Model // ArcFace recognition model
	path     string      // recognition model path (for diagnostics)
	detPath  string      // detector model path (for diagnostics)
}

// NewSCRFDEmbeddingFaceProvider loads both the SCRFD detector and the ArcFace
// recognition ONNX model. It validates both on load so a broken file is
// caught immediately.
func NewSCRFDEmbeddingFaceProvider(detectorPath, recognizerPath string) (*SCRFDEmbeddingFaceProvider, error) {
	det, err := NewSCRFDDetector(detectorPath)
	if err != nil {
		return nil, fmt.Errorf("SCRFD detector: %w", err)
	}

	m, err := onnx.Load(recognizerPath)
	if err != nil {
		return nil, fmt.Errorf("load ArcFace model %s: %w", recognizerPath, err)
	}
	// Dry-run to confirm shape and detect obvious corruption.
	dummyImg := image.NewRGBA(image.Rect(0, 0, 8, 8))
	dummyLMs := defaultLandmarks(image.Rect(0, 0, 8, 8))
	out, err := m.Run(alignedTensorLM(dummyImg, dummyLMs))
	if err != nil {
		return nil, fmt.Errorf("ArcFace model %s dry-run: %w", recognizerPath, err)
	}
	if len(out.Data) != embedDimension {
		return nil, fmt.Errorf("ArcFace model %s: embedding has %d values, want %d",
			recognizerPath, len(out.Data), embedDimension)
	}

	return &SCRFDEmbeddingFaceProvider{
		detector: det,
		model:    m,
		path:     recognizerPath,
		detPath:  detectorPath,
	}, nil
}

// Name is the stable provider identifier.
func (*SCRFDEmbeddingFaceProvider) Name() string { return "scrfd_arcface" }

// Version is bumped whenever the alignment algorithm or model changes.
func (*SCRFDEmbeddingFaceProvider) Version() int { return 5 }

// DefaultThreshold is the cosine similarity at which two ArcFace R50
// embeddings count as the same person. Measured on real photos, one person
// across pose, lighting and glasses scores about 0.4–0.9 while different
// people stay below about 0.35; grouping also needs two supporting matches
// before a face joins a group (see face_cluster.go), which keeps 0.45 safe.
func (*SCRFDEmbeddingFaceProvider) DefaultThreshold() float64 { return 0.45 }

// Describe returns a human-readable summary.
func (p *SCRFDEmbeddingFaceProvider) Describe() string {
	return "SCRFD face detection + 5-point landmark alignment + ArcFace R50 512-d embedding (local)"
}

// Detect runs SCRFD on img.
func (p *SCRFDEmbeddingFaceProvider) Detect(img image.Image) ([]FaceBox, error) {
	return p.detector.Detect(img)
}

// Embed aligns the face on its 5 landmarks and runs the ArcFace network.
func (p *SCRFDEmbeddingFaceProvider) Embed(img image.Image, box FaceBox) ([]float32, error) {
	if box.Width <= 0 || box.Height <= 0 {
		return nil, fmt.Errorf("degenerate face box %+v", box)
	}
	lms := box.Landmarks
	// Fall back to geometric defaults when landmarks are zero (legacy path or
	// non-landmark detector).
	if lms == ([5][2]float32{}) {
		lms = defaultLandmarks(image.Rect(box.X, box.Y, box.X+box.Width, box.Y+box.Height))
	}
	out, err := p.model.Run(alignedTensorLM(img, lms))
	if err != nil {
		return nil, err
	}
	return l2Normalize(out.Data), nil
}

// defaultLandmarks estimates the 5-point landmarks geometrically from a box
// when the detector did not provide them. This keeps old stored faces
// partially compatible when upgrading.
func defaultLandmarks(r image.Rectangle) [5][2]float32 {
	x0 := float32(r.Min.X)
	y0 := float32(r.Min.Y)
	w := float32(r.Dx())
	h := float32(r.Dy())
	return [5][2]float32{
		{x0 + 0.3*w, y0 + 0.40*h},  // left eye
		{x0 + 0.7*w, y0 + 0.40*h},  // right eye
		{x0 + 0.5*w, y0 + 0.60*h},  // nose
		{x0 + 0.35*w, y0 + 0.75*h}, // left mouth
		{x0 + 0.65*w, y0 + 0.75*h}, // right mouth
	}
}

// alignedTensorLM warps the source image so the 5 detected landmarks land on
// the ArcFace reference positions inside a 112×112 crop. It uses a full
// similarity transform (uniform scale + rotation + translation) estimated from
// the two eye points (the most reliable pair) and returns the NCHW float32
// input tensor expected by InsightFace networks: (v − 127.5) / 127.5.
func alignedTensorLM(img image.Image, lms [5][2]float32) *onnx.Tensor {
	// Estimate similarity transform from the 5 src→dst point correspondences
	// using the closed-form least-squares solution (same as cv2.estimateAffinePartial2D
	// in similarity mode). This gives a [cos, -sin, tx; sin, cos, ty] matrix.
	cos, sin, tx, ty := similarityTransform5(lms, arcfaceRef)

	b := img.Bounds()
	t := &onnx.Tensor{Shape: []int{1, 3, alignSize, alignSize}, Data: make([]float32, 3*alignSize*alignSize)}
	plane := alignSize * alignSize
	for y := 0; y < alignSize; y++ {
		for x := 0; x < alignSize; x++ {
			// Inverse transform: output (x,y) → source coordinates
			sx := cos*float64(x) + sin*float64(y) + tx
			sy := -sin*float64(x) + cos*float64(y) + ty
			r, g, bl := bilinear(img, b, sx, sy)
			i := y*alignSize + x
			t.Data[i] = (r - 127.5) / 127.5
			t.Data[plane+i] = (g - 127.5) / 127.5
			t.Data[2*plane+i] = (bl - 127.5) / 127.5
		}
	}
	return t
}

// similarityTransform5 estimates the inverse similarity transform (from
// output aligned space back to source image) that maps the ArcFace reference
// points to the detected landmarks. It uses the closed-form solution for
// 5-point similarity (least-squares normal equations on all 5 pairs).
//
// Returns (a, b, tx, ty) such that:
//
//	src_x = a * out_x + b * out_y + tx
//	src_y = -b * out_x + a * out_y + ty
//
// where (a=cosθ·s, b=sinθ·s) captures rotation and scale.
func similarityTransform5(src [5][2]float32, dst [5][2]float64) (a, b, tx, ty float64) {
	// Solve:  [ Σ(xi²+yi²)  0         Σxi  Σyi ] [a ]   [Σ(xi*ui + yi*vi)]
	//         [ 0           Σ(xi²+yi²) Σyi -Σxi ] [b ]   [Σ(yi*ui - xi*vi)]
	//         [ Σxi         Σyi        N    0   ] [tx]   [Σui             ]
	//         [ Σyi        -Σxi        0    N   ] [ty]   [Σvi             ]
	// where (xi,yi) are reference (dst) points and (ui,vi) are source points.
	N := 5
	var sxx, sx, sy, sxu, sxv, su, sv float64
	for i := 0; i < N; i++ {
		xi, yi := dst[i][0], dst[i][1]
		ui, vi := float64(src[i][0]), float64(src[i][1])
		sxx += xi*xi + yi*yi
		sx += xi
		sy += yi
		sxu += xi*ui + yi*vi
		sxv += yi*ui - xi*vi
		su += ui
		sv += vi
	}
	_ = sxu // used below

	// With N=5, the normal equations reduce to:
	//   sxx·a  + sx·tx + sy·ty = sxu
	//   sxx·b  + sy·tx - sx·ty = sxv
	//   sx·a   + sy·b  + N·tx  = su
	//   sy·a   - sx·b  + N·ty  = sv
	//
	// Solve for a, b, tx, ty using Cramer's rule on the 4×4 system,
	// or (simpler) recognize that with the symmetric structure:
	//
	//   det = sxx * float64(N) - sx*sx - sy*sy
	if sxx == 0 {
		// degenerate: identity
		return 1, 0, 0, 0
	}
	det := sxx*float64(N) - sx*sx - sy*sy
	if math.Abs(det) < 1e-10 {
		return 1, 0, 0, 0
	}
	a = (float64(N)*sxu - sx*su - sy*sv) / det
	b = (float64(N)*sxv - sy*su + sx*sv) / det
	tx = (su - a*sx - b*sy) / float64(N)
	ty = (sv + b*sx - a*sy) / float64(N)
	return
}

// bilinear samples the image at a fractional position, clamping at the edges,
// and returns 0..255 channel values.
func bilinear(img image.Image, b image.Rectangle, x, y float64) (r, g, bl float32) {
	x0, y0 := math.Floor(x), math.Floor(y)
	fx, fy := float32(x-x0), float32(y-y0)
	px := func(ix, iy int) (float32, float32, float32) {
		if ix < b.Min.X {
			ix = b.Min.X
		}
		if ix >= b.Max.X {
			ix = b.Max.X - 1
		}
		if iy < b.Min.Y {
			iy = b.Min.Y
		}
		if iy >= b.Max.Y {
			iy = b.Max.Y - 1
		}
		cr, cg, cb, _ := img.At(ix, iy).RGBA()
		return float32(cr >> 8), float32(cg >> 8), float32(cb >> 8)
	}
	ix, iy := int(x0), int(y0)
	r00, g00, b00 := px(ix, iy)
	r10, g10, b10 := px(ix+1, iy)
	r01, g01, b01 := px(ix, iy+1)
	r11, g11, b11 := px(ix+1, iy+1)
	mix := func(a, b, c, d float32) float32 {
		return (a*(1-fx)+b*fx)*(1-fy) + (c*(1-fx)+d*fx)*fy
	}
	return mix(r00, r10, r01, r11), mix(g00, g10, g01, g11), mix(b00, b10, b01, b11)
}

func l2Normalize(v []float32) []float32 {
	var n float64
	for _, x := range v {
		n += float64(x) * float64(x)
	}
	out := make([]float32, len(v))
	if n == 0 {
		return out
	}
	inv := float32(1 / math.Sqrt(n))
	for i, x := range v {
		out[i] = x * inv
	}
	return out
}
