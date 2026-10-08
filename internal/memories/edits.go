package memories

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
)

// Edits are the non-destructive, Memory-specific visual changes applied to a
// referenced image. They are presentation data: the original file is never
// touched. Clients render them directly (CSS transforms and filters on the
// web), and when a user enables "create edited copies" the server renders the
// same pipeline into a derived JPEG under .cairn/memory-media/.
//
// The pipeline order is fixed and shared by every renderer:
//
//  1. EXIF orientation (what browsers already show for the original)
//  2. Rotation, clockwise, in multiples of 90°
//  3. Crop, a normalized rectangle in the rotated image's coordinates
//  4. Filter preset, then Adjustments (see FilterOps)
type Edits struct {
	Crop        *Crop
	Rotation    int
	Filter      string
	Adjustments Adjustments
}

// Crop is a rectangle in normalized coordinates (0..1) of the rotated image.
// A nil *Crop means "no crop".
type Crop struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// Adjustments are slider values in [-100, 100]; 0 is neutral.
type Adjustments struct {
	Brightness int `json:"brightness"`
	Contrast   int `json:"contrast"`
	Saturation int `json:"saturation"`
}

// Filter preset names. FilterOriginal is the neutral value.
const (
	FilterOriginal = "original"
	FilterWarm     = "warm"
	FilterCool     = "cool"
	FilterBW       = "bw"
	FilterSoft     = "soft"
	FilterContrast = "contrast"
)

// FilterOp is one CSS-filter-equivalent primitive. Kind is one of
// "brightness", "contrast", "saturate", "grayscale", "sepia" or
// "hue-rotate" (Amount in degrees for hue-rotate, a factor otherwise). The
// math follows the W3C Filter Effects shorthand definitions so a browser
// applying `filter:` and the server rendering a derived copy agree.
type FilterOp struct {
	Kind   string
	Amount float64
}

// filterPresets define each preset as a sequence of primitives. The web
// client mirrors this table in web/src/memories/edits.ts; keep them in sync.
var filterPresets = map[string][]FilterOp{
	FilterOriginal: nil,
	FilterWarm:     {{"sepia", 0.22}, {"saturate", 1.15}, {"brightness", 1.03}},
	FilterCool:     {{"saturate", 0.9}, {"hue-rotate", 12}, {"brightness", 1.02}},
	FilterBW:       {{"grayscale", 1}, {"contrast", 1.12}},
	FilterSoft:     {{"contrast", 0.86}, {"brightness", 1.06}, {"saturate", 0.85}},
	FilterContrast: {{"contrast", 1.22}, {"saturate", 1.12}},
}

// IsKnownFilter reports whether name is a supported preset.
func IsKnownFilter(name string) bool {
	_, ok := filterPresets[name]
	return ok
}

// FilterOps returns the full primitive sequence for edits: the preset first,
// then the adjustment sliders (brightness ±50%, contrast ±50%, saturation
// ±100%), skipping neutral steps.
func (e Edits) FilterOps() []FilterOp {
	ops := append([]FilterOp(nil), filterPresets[e.normalizedFilter()]...)
	if b := e.Adjustments.Brightness; b != 0 {
		ops = append(ops, FilterOp{"brightness", 1 + float64(b)/200})
	}
	if c := e.Adjustments.Contrast; c != 0 {
		ops = append(ops, FilterOp{"contrast", 1 + float64(c)/200})
	}
	if s := e.Adjustments.Saturation; s != 0 {
		ops = append(ops, FilterOp{"saturate", 1 + float64(s)/100})
	}
	return ops
}

func (e Edits) normalizedFilter() string {
	if e.Filter == "" {
		return FilterOriginal
	}
	return e.Filter
}

// IsDefault reports whether the edits change nothing visually. A default
// image is always served from the original — no derived copy is made.
func (e Edits) IsDefault() bool {
	return e.Crop == nil && e.Rotation == 0 && e.normalizedFilter() == FilterOriginal &&
		e.Adjustments == (Adjustments{})
}

// Validate checks ranges. Crops are clamped to the unit square first, so a
// rectangle that overshoots by a rounding error is accepted.
func (e *Edits) Validate() error {
	if e.Filter == "" {
		e.Filter = FilterOriginal
	}
	if !IsKnownFilter(e.Filter) {
		return &ValidationError{msg: fmt.Sprintf("unknown filter %q", e.Filter)}
	}
	switch e.Rotation {
	case 0, 90, 180, 270:
	default:
		return &ValidationError{msg: "rotation must be 0, 90, 180 or 270"}
	}
	for _, v := range []int{e.Adjustments.Brightness, e.Adjustments.Contrast, e.Adjustments.Saturation} {
		if v < -100 || v > 100 {
			return &ValidationError{msg: "adjustments must be between -100 and 100"}
		}
	}
	if c := e.Crop; c != nil {
		for _, v := range []float64{c.X, c.Y, c.Width, c.Height} {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return &ValidationError{msg: "crop values must be finite"}
			}
		}
		c.X = clamp01(c.X)
		c.Y = clamp01(c.Y)
		c.Width = math.Min(c.Width, 1-c.X)
		c.Height = math.Min(c.Height, 1-c.Y)
		if c.Width < 0.01 || c.Height < 0.01 {
			return &ValidationError{msg: "crop is too small"}
		}
		// A crop covering the whole image is no crop.
		if c.X < 1e-4 && c.Y < 1e-4 && c.Width > 1-1e-4 && c.Height > 1-1e-4 {
			e.Crop = nil
		}
	}
	return nil
}

// Signature is a stable fingerprint of the visual edits. A derived copy is
// valid only while its signature matches the image's current edits (and the
// source file it was rendered from).
func (e Edits) Signature(sourceFileID string) string {
	crop := "none"
	if e.Crop != nil {
		crop = fmt.Sprintf("%.5f,%.5f,%.5f,%.5f", e.Crop.X, e.Crop.Y, e.Crop.Width, e.Crop.Height)
	}
	raw := fmt.Sprintf("v1|%s|%s|%d|%s|%d,%d,%d", sourceFileID, crop, e.Rotation, e.normalizedFilter(),
		e.Adjustments.Brightness, e.Adjustments.Contrast, e.Adjustments.Saturation)
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])[:16]
}

func clamp01(v float64) float64 { return math.Max(0, math.Min(1, v)) }
