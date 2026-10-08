package ml

import (
	"image"
	"image/color"
	"math"
	"testing"
)

// A face rotated in the photo must be turned upright: after alignment the
// eyes sit at the reference positions whatever the tilt.
func TestAlignmentUprightsRotatedFace(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 200, 200))
	for y := 0; y < 200; y++ {
		for x := 0; x < 200; x++ {
			img.Set(x, y, color.RGBA{20, 20, 20, 255})
		}
	}

	// Rotate all 5 reference landmarks by 30° around (100,100) scaled by 1.0.
	// This produces a consistent set of 5 source points for the same face.
	angle := 30.0 * math.Pi / 180.0
	cos, sin := math.Cos(angle), math.Sin(angle)
	cx, cy := 100.0, 100.0

	// Scale factor: map the reference inter-eye distance to 60px.
	refInterEye := math.Hypot(arcfaceRef[1][0]-arcfaceRef[0][0], arcfaceRef[1][1]-arcfaceRef[0][1])
	scale := 60.0 / refInterEye

	// Reference centroid (average of 5 points).
	var rcx, rcy float64
	for _, p := range arcfaceRef {
		rcx += p[0]
		rcy += p[1]
	}
	rcx /= 5
	rcy /= 5

	// Map each reference point into the rotated image frame.
	var lms [5][2]float32
	for i, p := range arcfaceRef {
		// Center the reference point.
		dx := (p[0] - rcx) * scale
		dy := (p[1] - rcy) * scale
		// Rotate.
		srcX := cos*dx - sin*dy + cx
		srcY := sin*dx + cos*dy + cy
		lms[i] = [2]float32{float32(srcX), float32(srcY)}
		// Mark the eye points white.
		if i < 2 {
			for ddy := -3; ddy <= 3; ddy++ {
				for ddx := -3; ddx <= 3; ddx++ {
					img.Set(int(srcX)+ddx, int(srcY)+ddy, color.White)
				}
			}
		}
	}

	tn := alignedTensorLM(img, lms)

	// After alignment the left reference eye is at ~(38,52) and right at ~(74,52).
	// Those output pixels should map back to the white dots in source.
	atRef := func(x, y int) float32 { return tn.Data[y*alignSize+x] }
	if atRef(38, 52) < 0.5 || atRef(74, 52) < 0.5 {
		t.Fatalf("eyes not at reference positions: left=%.2f right=%.2f (want > 0.5)",
			atRef(38, 52), atRef(74, 52))
	}
}

func TestEmbeddingNormalisedAndCosine(t *testing.T) {
	v := l2Normalize([]float32{3, 4})
	if math.Abs(float64(v[0]*v[0]+v[1]*v[1])-1) > 1e-6 {
		t.Fatalf("not unit: %v", v)
	}
	if DescriptorCosine(v, v) < 0.999 {
		t.Fatal("self similarity")
	}
}

func unit(v ...float32) []float32 { return l2Normalize(v) }

// After two looks of one person are merged, a face resembling only the second
// look must still match, which an averaged centroid alone would miss.
func TestMatchScoreRecognisesEitherMergedLook(t *testing.T) {
	lookA := unit(1, 0, 0, 0)
	lookB := unit(0, 1, 0, 0)
	ex := []Exemplar{{Descriptor: lookA}, {Descriptor: lookA}, {Descriptor: lookB}, {Descriptor: lookB}}
	centroid := unit(1, 1, 0, 0)

	if got := matchScore(unit(0, 1, 0.1, 0), centroid, ex); got < 0.9 {
		t.Fatalf("second look scored %.2f, want a match", got)
	}
	if got := matchScore(unit(0, 0, 1, 0), centroid, ex); got > 0.2 {
		t.Fatalf("stranger scored %.2f, want a miss", got)
	}
}

// One odd photo must not pull a stranger in: two supporting faces are needed.
func TestMatchScoreNeedsTwoSupportingFaces(t *testing.T) {
	ex := []Exemplar{{Descriptor: unit(1, 0, 0, 0)}, {Descriptor: unit(0, 0, 1, 0)}}
	centroid := unit(1, 0, 1, 0)
	if got := matchScore(unit(1, 0, 0, 0), centroid, ex); got > 0.8 {
		t.Fatalf("a single close face gave %.2f", got)
	}
}

func TestGroupSimilarity(t *testing.T) {
	a := []Exemplar{{Descriptor: unit(1, 0, 0)}, {Descriptor: unit(1, 0.1, 0)}}
	same := []Exemplar{{Descriptor: unit(1, 0.05, 0)}, {Descriptor: unit(1, 0, 0.05)}}
	other := []Exemplar{{Descriptor: unit(0, 0, 1)}, {Descriptor: unit(0, 0.1, 1)}}
	if groupSimilarity(a, same) < 0.95 || groupSimilarity(a, other) > 0.3 {
		t.Fatalf("same=%.2f other=%.2f", groupSimilarity(a, same), groupSimilarity(a, other))
	}
}

// TestSimilarityTransform5Identity checks that a transform with identical
// src and dst points produces an identity-like mapping.
func TestSimilarityTransform5Identity(t *testing.T) {
	// Use the reference points as both src and dst.
	var src [5][2]float32
	for i, p := range arcfaceRef {
		src[i] = [2]float32{float32(p[0]), float32(p[1])}
	}
	a, b, tx, ty := similarityTransform5(src, arcfaceRef)
	// Should be close to identity: a≈1, b≈0, tx≈0, ty≈0
	if math.Abs(a-1) > 0.01 || math.Abs(b) > 0.01 {
		t.Fatalf("identity transform: a=%.4f b=%.4f, want a≈1 b≈0", a, b)
	}
	if math.Abs(tx) > 0.5 || math.Abs(ty) > 0.5 {
		t.Fatalf("identity translation: tx=%.4f ty=%.4f, want ≈0", tx, ty)
	}
}
