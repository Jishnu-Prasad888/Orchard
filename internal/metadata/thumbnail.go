package metadata

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"time"

	"github.com/Jishnu-Prasad888/Cairn/internal/crypto"
	"github.com/Jishnu-Prasad888/Cairn/internal/media"
	"github.com/Jishnu-Prasad888/Cairn/internal/safeimage"
	"golang.org/x/image/draw"
)

const (
	// ThumbnailSize is the maximum dimension (width or height) for thumbnails.
	ThumbnailSize = 400
	// ThumbnailQuality is the JPEG quality for generated thumbnails (0-100).
	ThumbnailQuality = 82
	// ThumbsDirName is the thumbnails subdirectory name inside .cairn/.
	ThumbsDirName = "thumbs"
)

// ThumbPath returns the absolute path for the thumbnail of a file identified
// by fileID, given the library's .cairn directory.
func ThumbPath(cairnDir, fileID string) string {
	return filepath.Join(cairnDir, ThumbsDirName, fileID+".jpg")
}

// GenerateThumbnail creates a JPEG thumbnail from srcPath and writes it to
// ThumbPath(cairnDir, fileID). Photos are decoded directly; videos use their
// first frame, extracted with ffmpeg when it is installed. The original file
// is never modified; when keys are enabled the on-disk thumbnail is sealed
// with the at-rest key. Returns (true, nil) when the thumbnail was written,
// (false, nil) when the file has no thumbnail this server can make.
func GenerateThumbnail(srcPath, cairnDir, fileID string, keys *crypto.Keys) (bool, error) {
	if media.DetectMediaType(srcPath) == media.MediaTypeVideo {
		if !VideoFramesAvailable() {
			return false, nil
		}
		frame, err := extractVideoFrame(context.Background(), srcPath)
		if err != nil {
			// ffmpeg cannot decode this file (corrupt, unrecognised codec,
			// not actually a video, etc.) — skip silently rather than
			// treating it as a server error.
			return false, nil
		}
		if err := writeThumb(frame, cairnDir, fileID, keys); err != nil {
			return false, err
		}
		return true, nil
	}

	src, err := os.Open(srcPath)
	if err != nil {
		return false, fmt.Errorf("open source for thumbnail: %w", err)
	}
	defer func() { _ = src.Close() }()

	img, _, err := safeimage.Decode(src)
	if err != nil {
		// Not a supported image (or exceeds safety limits) — skip silently
		// (audio, docs, etc.).
		return false, nil
	}
	if err := writeThumb(img, cairnDir, fileID, keys); err != nil {
		return false, err
	}
	return true, nil
}

// writeThumb resizes img, encodes it as JPEG and commits it atomically to
// ThumbPath(cairnDir, fileID), sealed when keys are enabled.
func writeThumb(img image.Image, cairnDir, fileID string, keys *crypto.Keys) error {
	thumb := resizeToFit(img, ThumbnailSize)

	thumbsDir := filepath.Join(cairnDir, ThumbsDirName)
	if err := os.MkdirAll(thumbsDir, 0o755); err != nil {
		return fmt.Errorf("create thumbs dir: %w", err)
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, thumb, &jpeg.Options{Quality: ThumbnailQuality}); err != nil {
		return fmt.Errorf("encode thumbnail: %w", err)
	}
	data := keys.Seal(buf.Bytes())

	destPath := ThumbPath(cairnDir, fileID)
	tmp := destPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write thumbnail: %w", err)
	}
	if err := os.Rename(tmp, destPath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("commit thumbnail: %w", err)
	}
	return nil
}

// ReadThumb reads and decrypts the thumbnail for fileID, returning the JPEG
// bytes and the file's modification time. A missing thumbnail returns an
// os.ErrNotExist-wrapped error; legacy plaintext thumbnails are returned
// unchanged even when keys are enabled.
func ReadThumb(cairnDir, fileID string, keys *crypto.Keys) ([]byte, time.Time, error) {
	path := ThumbPath(cairnDir, fileID)
	info, err := os.Stat(path)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("stat thumbnail: %w", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, info.ModTime(), fmt.Errorf("read thumbnail: %w", err)
	}
	plain, err := keys.Open(data)
	if err != nil {
		return nil, info.ModTime(), fmt.Errorf("decrypt thumbnail: %w", err)
	}
	return plain, info.ModTime(), nil
}

// resizeToFit returns a new image scaled so the larger dimension equals
// maxDim, preserving aspect ratio. Uses high-quality BiLinear resampling.
// If the image is already smaller than maxDim it is returned unchanged.
func resizeToFit(src image.Image, maxDim int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 {
		return src
	}
	if w <= maxDim && h <= maxDim {
		return src
	}

	var newW, newH int
	if w >= h {
		newW = maxDim
		newH = int(float64(h) * float64(maxDim) / float64(w))
	} else {
		newH = maxDim
		newW = int(float64(w) * float64(maxDim) / float64(h))
	}
	if newW < 1 {
		newW = 1
	}
	if newH < 1 {
		newH = 1
	}

	dst := image.NewRGBA(image.Rect(0, 0, newW, newH))
	draw.BiLinear.Scale(dst, dst.Bounds(), src, b, draw.Over, nil)
	return dst
}
