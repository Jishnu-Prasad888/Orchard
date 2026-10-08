// Package metadata extracts media metadata from files.
//
// All extraction is read-only: the original file is never written, moved, or
// modified. Extracted data is purely derived information that can be
// regenerated at any time.
package metadata

import (
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"math"
	"os"
	"time"

	"github.com/rwcarlsen/goexif/exif"
)

// MediaInfo holds the extracted metadata for one media file.
type MediaInfo struct {
	// Image dimensions (0 if not applicable or not extractable).
	Width  int
	Height int

	// TakenAt is the capture timestamp from EXIF DateTimeOriginal.
	// Zero when not present.
	TakenAt time.Time

	// Camera information from EXIF.
	CameraMake  string
	CameraModel string

	// GPS coordinates from EXIF. Both are 0 when not present.
	Latitude  float64
	Longitude float64

	// HasGPS is true when GPS coordinates were successfully extracted.
	HasGPS bool

	// Orientation from EXIF (1–8, 0 when not present).
	Orientation int

	// MIMEType as detected from the file content.
	MIMEType string
}

// ExtractFromFile reads metadata from the file at absPath without modifying
// it. It is safe to call on large files — image dimensions are read from the
// header only; the full image is not decoded for EXIF extraction.
func ExtractFromFile(absPath string) (*MediaInfo, error) {
	f, err := os.Open(absPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return ExtractFromReader(f)
}

// ExtractFromReader extracts metadata from a reader. The reader must be
// seekable (implements io.ReadSeeker) so EXIF and dimensions can be read
// in separate passes.
func ExtractFromReader(r io.ReadSeeker) (*MediaInfo, error) {
	info := &MediaInfo{}

	// --- Pass 1: image dimensions ---
	if cfg, _, err := image.DecodeConfig(r); err == nil {
		info.Width = cfg.Width
		info.Height = cfg.Height
	}

	// Reset to beginning for EXIF pass.
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return info, nil // seekable read failed; return what we have
	}

	// --- Pass 2: EXIF ---
	x, err := exif.Decode(r)
	if err != nil {
		// Non-fatal: many files (PNG, video, plain files) have no EXIF.
		return info, nil
	}

	// DateTimeOriginal — capture time.
	if t, err := x.DateTime(); err == nil {
		info.TakenAt = t.UTC()
	}

	// Camera make and model.
	if tag, err := x.Get(exif.Make); err == nil {
		info.CameraMake, _ = tag.StringVal()
	}
	if tag, err := x.Get(exif.Model); err == nil {
		info.CameraModel, _ = tag.StringVal()
	}

	// Orientation.
	if tag, err := x.Get(exif.Orientation); err == nil {
		if v, err := tag.Int(0); err == nil {
			info.Orientation = v
		}
	}

	// GPS coordinates.
	if lat, lon, err := x.LatLong(); err == nil {
		if !math.IsNaN(lat) && !math.IsNaN(lon) {
			info.Latitude = lat
			info.Longitude = lon
			info.HasGPS = true
		}
	}

	return info, nil
}
