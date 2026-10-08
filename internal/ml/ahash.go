package ml

import (
	"fmt"
	"image"
	_ "image/gif"  // register GIF decoder
	_ "image/jpeg" // register JPEG decoder
	_ "image/png"  // register PNG decoder
	"os"

	"github.com/Jishnu-Prasad888/Cairn/internal/safeimage"
	"golang.org/x/image/draw"
)

// AverageHashProvider is the built-in similarity provider: a 64-bit average
// hash over an 8x8 grayscale downsample. It is dependency-free, fast, and
// adequate for near-duplicate / same-scene queries on a personal library.
type AverageHashProvider struct{}

// Name returns the provider identifier.
func (AverageHashProvider) Name() string { return "average_hash" }

// Version is the algorithm version. Bump when the hash changes.
func (AverageHashProvider) Version() int { return 1 }

// Describe returns a short human-readable summary.
func (AverageHashProvider) Describe() string {
	return "64-bit average hash over 8x8 grayscale (perceptual similarity)"
}

// Signature computes the average hash of an image: downsample to 8x8
// grayscale, then encode one bit per pixel indicating whether the pixel is
// above the mean. Returns a 64-bit signature in raster order.
func (AverageHashProvider) Signature(img image.Image) (uint64, error) {
	thumb := image.NewGray(image.Rect(0, 0, 8, 8))
	draw.CatmullRom.Scale(thumb, thumb.Bounds(), img, img.Bounds(), draw.Over, nil)

	// Mean of the 64 pixels. Average over the grayscale samples avoids
	// intermediate overflow for any image size.
	var sum uint64
	pix := thumb.Pix
	for _, v := range pix {
		sum += uint64(v)
	}
	mean := sum / uint64(len(pix))

	var sig uint64
	for i, v := range pix {
		if uint64(v) >= mean {
			sig |= 1 << uint(i)
		}
	}
	return sig, nil
}

// OpenImage opens and decodes an image file, reporting a descriptive error on
// decode failure. The caller is responsible for closing the file.
func openImage(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open image: %w", err)
	}
	img, _, err := safeimage.Decode(f)
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("decode image: %w", err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("close image: %w", err)
	}
	return img, nil
}
