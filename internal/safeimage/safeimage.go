// Package safeimage decodes images with allocation bounds so that decoding
// attacker-controlled files cannot turn into a memory-exhaustion vector.
//
// Go's standard image packages do not cap decoded dimensions, so a tiny JPEG
// or PNG header claiming a 30k×30k frame is enough to make image.Decode
// allocate gigabytes. safeimage reads the header first (image.DecodeConfig,
// which does not allocate pixel buffers), rejects oversized frames, and only
// then decodes.
package safeimage

import (
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
)

const (
	// MaxPixels bounds the total pixel count of any decoded image.
	MaxPixels = 50_000_000
	// MaxDimension bounds either side of a decoded image.
	MaxDimension = 30_000
)

// ErrTooLarge is returned when an image's declared dimensions exceed the
// safety limits.
var ErrTooLarge = errors.New("image dimensions exceed safety limits")

// Decode decodes r after verifying its declared dimensions. r must be
// seekable so the header can be read and rewound. Callers that hold an
// *os.File (as every site in the codebase does) can pass it directly.
func Decode(r io.ReadSeeker) (image.Image, string, error) {
	cfg, _, err := image.DecodeConfig(r)
	if err != nil {
		return nil, "", err
	}
	if cfg.Width > MaxDimension || cfg.Height > MaxDimension {
		return nil, "", fmt.Errorf("%w: %dx%d", ErrTooLarge, cfg.Width, cfg.Height)
	}
	if cfg.Width > 0 && cfg.Height > 0 && cfg.Width*cfg.Height > MaxPixels {
		return nil, "", fmt.Errorf("%w: %dx%d", ErrTooLarge, cfg.Width, cfg.Height)
	}
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return nil, "", fmt.Errorf("safeimage rewind: %w", err)
	}
	return image.Decode(r)
}

// DecodeConfig returns the declared dimensions of an image without decoding
// pixel data. It applies the same caps so callers can reject oversized files
// before any further processing.
func DecodeConfig(r io.Reader) (image.Config, string, error) {
	cfg, format, err := image.DecodeConfig(r)
	if err != nil {
		return cfg, format, err
	}
	if cfg.Width > MaxDimension || cfg.Height > MaxDimension {
		return cfg, format, fmt.Errorf("%w: %dx%d", ErrTooLarge, cfg.Width, cfg.Height)
	}
	if cfg.Width > 0 && cfg.Height > 0 && cfg.Width*cfg.Height > MaxPixels {
		return cfg, format, fmt.Errorf("%w: %dx%d", ErrTooLarge, cfg.Width, cfg.Height)
	}
	return cfg, format, nil
}
