package ml

import (
	"image"
	"math"
	"sort"

	"golang.org/x/image/draw"
)

// PerceptualHashProvider is the built-in similarity provider: a 64-bit
// DCT-based perceptual hash (pHash).
//
// An average hash compares an 8x8 thumbnail's pixels to its mean, so a small
// brightness change, a re-encode or a slight resize flips many bits. The DCT
// hash instead keeps only the lowest spatial frequencies of a 32x32 grayscale
// copy — the coarse structure of the picture — which is what survives
// recompression, resizing, small colour or exposure edits and light
// sharpening. Identical pixels always give an identical hash.
type PerceptualHashProvider struct{}

const (
	phashSize = 32 // side of the grayscale image the DCT runs on
	phashLow  = 8  // side of the low-frequency block that becomes the hash
)

// Name returns the provider identifier.
func (PerceptualHashProvider) Name() string { return "perceptual_hash" }

// Version is the algorithm version. Bump when the hash changes.
func (PerceptualHashProvider) Version() int { return 2 }

// Describe returns a short human-readable summary.
func (PerceptualHashProvider) Describe() string {
	return "64-bit DCT perceptual hash over 32x32 grayscale (robust to resizing, re-encoding and small edits)"
}

// dctBasis[k][n] = cos(pi/N * (n + 0.5) * k), the DCT-II basis for N=phashSize.
var dctBasis = func() [phashLow][phashSize]float64 {
	var b [phashLow][phashSize]float64
	for k := 0; k < phashLow; k++ {
		for n := 0; n < phashSize; n++ {
			b[k][n] = math.Cos(math.Pi / phashSize * (float64(n) + 0.5) * float64(k))
		}
	}
	return b
}()

// Signature computes the pHash of img.
func (PerceptualHashProvider) Signature(img image.Image) (uint64, error) {
	small := image.NewGray(image.Rect(0, 0, phashSize, phashSize))
	// CatmullRom averages over the source footprint when shrinking, so large
	// photos are not aliased the way a nearest-neighbour sample would be.
	draw.CatmullRom.Scale(small, small.Bounds(), img, img.Bounds(), draw.Src, nil)

	var px [phashSize][phashSize]float64
	for y := 0; y < phashSize; y++ {
		for x := 0; x < phashSize; x++ {
			px[y][x] = float64(small.Pix[y*small.Stride+x])
		}
	}

	// Separable 2-D DCT, computing only the phashLow x phashLow corner.
	var rows [phashSize][phashLow]float64
	for y := 0; y < phashSize; y++ {
		for u := 0; u < phashLow; u++ {
			var s float64
			for x := 0; x < phashSize; x++ {
				s += px[y][x] * dctBasis[u][x]
			}
			rows[y][u] = s
		}
	}
	var coef [phashLow * phashLow]float64
	for v := 0; v < phashLow; v++ {
		for u := 0; u < phashLow; u++ {
			var s float64
			for y := 0; y < phashSize; y++ {
				s += rows[y][u] * dctBasis[v][y]
			}
			coef[v*phashLow+u] = s
		}
	}

	// The DC term is the overall brightness; leaving it out of the median makes
	// the hash independent of exposure.
	ac := make([]float64, 0, len(coef)-1)
	ac = append(ac, coef[1:]...)
	sort.Float64s(ac)
	median := (ac[len(ac)/2-1] + ac[len(ac)/2]) / 2

	var sig uint64
	for i := 1; i < len(coef); i++ {
		if coef[i] > median {
			sig |= 1 << uint(i)
		}
	}
	return sig, nil
}
