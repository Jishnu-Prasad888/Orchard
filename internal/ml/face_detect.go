package ml

import (
	"fmt"
	"image"
	"math"
	"sort"

	"github.com/Jishnu-Prasad888/Cairn/internal/ml/onnx"
)

// SCRFDDetector runs SCRFD face detection from an ONNX model (e.g.
// det_10g.onnx, det_2.5g.onnx, det_500m.onnx from InsightFace) and returns
// face boxes with 5-point landmarks. The model is stateless after loading and
// safe for concurrent use.
//
// SCRFD architecture:
//   - Input:  1 × 3 × H × W  (resized to a multiple of 32, padded with grey)
//   - Outputs per FPN stride (8, 16, 32): score (H/N·W/N·A, 1), bbox (…, 4)
//     and optionally kps (…, 10); see scrfdGroupOutputs.
//
// where A is the number of anchors per location (2 for SCRFD).
type SCRFDDetector struct {
	model     *onnx.Model
	inputSize int     // longest side after resize (e.g. 640)
	nmsIoU    float64 // IoU threshold for NMS
	minScore  float64 // minimum class score (post-sigmoid)

	// strides and anchor counts decoded from the model graph outputs.
	// SCRFD always uses strides [8, 16, 32] with 2 anchors per location.
	strides  []int
	anchorsN int // anchors per location
}

const (
	scrfdInputSize   = 640
	scrfdNMSIoU      = 0.4
	scrfdMinScore    = 0.5
	scrfdAnchorsN    = 2
	scrfdNumLandmark = 5 // 5-point facial landmarks
)

// scrfdStrides are the FPN strides SCRFD uses.
var scrfdStrides = []int{8, 16, 32}

// NewSCRFDDetector loads an SCRFD ONNX model from path.
func NewSCRFDDetector(path string) (*SCRFDDetector, error) {
	m, err := onnx.Load(path)
	if err != nil {
		return nil, fmt.Errorf("load SCRFD model %s: %w", path, err)
	}
	// Sanity-check: the model must have 6 or 9 outputs (3 strides × 2 or 3
	// tensors per stride: score, bbox, and optionally kps).
	n := m.OutputCount()
	if n != 6 && n != 9 {
		return nil, fmt.Errorf("SCRFD model %s: want 6 or 9 outputs (3 strides × 2–3), got %d", path, n)
	}
	return &SCRFDDetector{
		model:     m,
		inputSize: scrfdInputSize,
		nmsIoU:    scrfdNMSIoU,
		minScore:  scrfdMinScore,
		strides:   scrfdStrides,
		anchorsN:  scrfdAnchorsN,
	}, nil
}

// Detect runs SCRFD on img and returns faces with 5-point landmarks.
func (d *SCRFDDetector) Detect(img image.Image) ([]FaceBox, error) {
	b := img.Bounds()
	origW, origH := b.Dx(), b.Dy()
	if origW < 8 || origH < 8 {
		return nil, nil
	}

	// Resize keeping aspect ratio, pad to multiple of 32.
	scale, netW, netH := scrfdPrepareSize(origW, origH, d.inputSize)
	inp := scrfdPreprocess(img, b, scale, netW, netH)

	outputs, err := d.model.RunMulti(inp)
	if err != nil {
		return nil, fmt.Errorf("SCRFD run: %w", err)
	}

	groups, err := scrfdGroupOutputs(outputs, len(d.strides))
	if err != nil {
		return nil, err
	}
	var candidates []scrfdCandidate
	for si, stride := range d.strides {
		g := groups[si]
		cs := scrfdDecodeStride(g.score, g.bbox, g.kps, stride, netW/stride, netH/stride, d.anchorsN, d.minScore)
		candidates = append(candidates, cs...)
	}

	// NMS
	kept := scrfdNMS(candidates, d.nmsIoU)

	// Scale back to original image coordinates.
	boxes := make([]FaceBox, 0, len(kept))
	for _, c := range kept {
		x0 := int(float64(c.x0) / scale)
		y0 := int(float64(c.y0) / scale)
		x1 := int(float64(c.x1) / scale)
		y1 := int(float64(c.y1) / scale)
		// Clamp
		if x0 < 0 {
			x0 = 0
		}
		if y0 < 0 {
			y0 = 0
		}
		if x1 > origW {
			x1 = origW
		}
		if y1 > origH {
			y1 = origH
		}
		if x1-x0 < 4 || y1-y0 < 4 {
			continue
		}
		var lm [5][2]float32
		for i := 0; i < 5; i++ {
			lm[i][0] = c.kps[i*2] / float32(scale)
			lm[i][1] = c.kps[i*2+1] / float32(scale)
		}
		boxes = append(boxes, FaceBox{
			X:          x0,
			Y:          y0,
			Width:      x1 - x0,
			Height:     y1 - y0,
			Confidence: float64(c.score),
			Landmarks:  lm,
		})
	}
	return boxes, nil
}

// scrfdPrepareSize computes the resize scale and padded network input size.
// Both netW and netH are multiples of 32. The scale is img→net.
func scrfdPrepareSize(w, h, maxSide int) (scale float64, netW, netH int) {
	scale = float64(maxSide) / math.Max(float64(w), float64(h))
	if scale > 1 {
		scale = 1
	}
	netW = roundUp(int(math.Floor(float64(w)*scale+0.5)), 32)
	netH = roundUp(int(math.Floor(float64(h)*scale+0.5)), 32)
	if netW < 32 {
		netW = 32
	}
	if netH < 32 {
		netH = 32
	}
	return
}

func roundUp(x, to int) int {
	return ((x + to - 1) / to) * to
}

// scrfdPreprocess scales img by scale into the top-left of a netW × netH
// canvas (the rest stays grey, which normalizes to 0) and returns an NCHW
// float32 tensor normalized to (v − 127.5) / 128.0. Each output pixel averages
// a 2×2 grid of source samples so large photos do not alias away small faces.
func scrfdPreprocess(img image.Image, bounds image.Rectangle, scale float64, netW, netH int) *onnx.Tensor {
	origW := bounds.Dx()
	origH := bounds.Dy()
	t := &onnx.Tensor{
		Shape: []int{1, 3, netH, netW},
		Data:  make([]float32, 3*netH*netW),
	}
	plane := netH * netW
	contentW := int(math.Floor(float64(origW)*scale + 0.5))
	contentH := int(math.Floor(float64(origH)*scale + 0.5))
	if contentW > netW {
		contentW = netW
	}
	if contentH > netH {
		contentH = netH
	}
	step := 1 / scale
	clamp := func(v, hi int) int {
		if v >= hi {
			return hi - 1
		}
		return v
	}
	for y := 0; y < contentH; y++ {
		for x := 0; x < contentW; x++ {
			var r, g, bl float32
			for _, oy := range [2]float64{0.25, 0.75} {
				sy := bounds.Min.Y + clamp(int((float64(y)+oy)*step), origH)
				for _, ox := range [2]float64{0.25, 0.75} {
					sx := bounds.Min.X + clamp(int((float64(x)+ox)*step), origW)
					cr, cg, cb, _ := img.At(sx, sy).RGBA()
					r += float32(cr >> 8)
					g += float32(cg >> 8)
					bl += float32(cb >> 8)
				}
			}
			i := y*netW + x
			t.Data[i] = (r/4 - 127.5) / 128.0
			t.Data[plane+i] = (g/4 - 127.5) / 128.0
			t.Data[2*plane+i] = (bl/4 - 127.5) / 128.0
		}
	}
	return t
}

// scrfdLevel holds the three head tensors of one FPN stride.
type scrfdLevel struct{ score, bbox, kps *onnx.Tensor }

// scrfdGroupOutputs pairs the raw graph outputs into per-stride levels
// (stride 8 first). InsightFace exports order outputs as all scores, then all
// boxes, then all landmarks, with tensors shaped (anchors, 1|4|10); rather than
// trusting that order, tensors are classified by trailing width and ranked by
// anchor count (the finest stride has the most anchors).
func scrfdGroupOutputs(outs []*onnx.Tensor, levels int) ([]scrfdLevel, error) {
	var scores, boxes, kps []*onnx.Tensor
	for _, o := range outs {
		if len(o.Shape) == 0 {
			return nil, fmt.Errorf("SCRFD output has no shape")
		}
		switch o.Shape[len(o.Shape)-1] {
		case 1:
			scores = append(scores, o)
		case 4:
			boxes = append(boxes, o)
		case 10:
			kps = append(kps, o)
		default:
			return nil, fmt.Errorf("SCRFD output has unexpected shape %v", o.Shape)
		}
	}
	if len(scores) != levels || len(boxes) != levels || (len(kps) != 0 && len(kps) != levels) {
		return nil, fmt.Errorf("SCRFD outputs: got %d score, %d bbox, %d kps tensors, want %d each",
			len(scores), len(boxes), len(kps), levels)
	}
	bySize := func(ts []*onnx.Tensor) {
		sort.SliceStable(ts, func(i, j int) bool {
			return len(ts[i].Data)/ts[i].Shape[len(ts[i].Shape)-1] > len(ts[j].Data)/ts[j].Shape[len(ts[j].Shape)-1]
		})
	}
	bySize(scores)
	bySize(boxes)
	bySize(kps)
	out := make([]scrfdLevel, levels)
	for i := range out {
		out[i] = scrfdLevel{score: scores[i], bbox: boxes[i]}
		if len(kps) > 0 {
			out[i].kps = kps[i]
		}
	}
	return out, nil
}

type scrfdCandidate struct {
	x0, y0, x1, y1 float32
	score          float32
	kps            [10]float32 // 5 landmarks × (x, y)
}

// scrfdDecodeStride decodes the score, bbox and kps tensors for one FPN stride.
// Rows are ordered (y, x, anchor); scores are already probabilities and box /
// landmark values are distances in units of stride from the anchor centre.
func scrfdDecodeStride(scoreT, bboxT, kpsT *onnx.Tensor,
	stride, fW, fH, anchorsN int, minScore float64) []scrfdCandidate {

	var out []scrfdCandidate
	fs := float32(stride)
	n := fH * fW * anchorsN
	if len(scoreT.Data) < n || len(bboxT.Data) < n*4 || (kpsT != nil && len(kpsT.Data) < n*10) {
		return nil
	}
	for ay := 0; ay < fH; ay++ {
		for ax := 0; ax < fW; ax++ {
			cx := float32(ax) * fs
			cy := float32(ay) * fs
			for an := 0; an < anchorsN; an++ {
				row := (ay*fW+ax)*anchorsN + an
				score := scoreT.Data[row]
				if float64(score) < minScore {
					continue
				}
				bb := bboxT.Data[row*4 : row*4+4]
				c := scrfdCandidate{
					x0: cx - bb[0]*fs, y0: cy - bb[1]*fs,
					x1: cx + bb[2]*fs, y1: cy + bb[3]*fs,
					score: score,
				}
				if kpsT != nil {
					kp := kpsT.Data[row*10 : row*10+10]
					for k := 0; k < 5; k++ {
						c.kps[k*2] = cx + kp[k*2]*fs
						c.kps[k*2+1] = cy + kp[k*2+1]*fs
					}
				} else {
					w := c.x1 - c.x0
					h := c.y1 - c.y0
					c.kps = [10]float32{
						c.x0 + w*0.3, c.y0 + h*0.4,
						c.x0 + w*0.7, c.y0 + h*0.4,
						c.x0 + w*0.5, c.y0 + h*0.6,
						c.x0 + w*0.3, c.y0 + h*0.75,
						c.x0 + w*0.7, c.y0 + h*0.75,
					}
				}
				out = append(out, c)
			}
		}
	}
	return out
}

// scrfdNMS performs greedy NMS and returns surviving detections, best-score first.
func scrfdNMS(cs []scrfdCandidate, iouThr float64) []scrfdCandidate {
	sort.Slice(cs, func(i, j int) bool { return cs[i].score > cs[j].score })
	keep := make([]scrfdCandidate, 0, len(cs))
	suppressed := make([]bool, len(cs))
	for i := range cs {
		if suppressed[i] {
			continue
		}
		keep = append(keep, cs[i])
		for j := i + 1; j < len(cs); j++ {
			if suppressed[j] {
				continue
			}
			if scrfdIoU(cs[i], cs[j]) > iouThr {
				suppressed[j] = true
			}
		}
	}
	return keep
}

func scrfdIoU(a, b scrfdCandidate) float64 {
	ix0 := math.Max(float64(a.x0), float64(b.x0))
	iy0 := math.Max(float64(a.y0), float64(b.y0))
	ix1 := math.Min(float64(a.x1), float64(b.x1))
	iy1 := math.Min(float64(a.y1), float64(b.y1))
	iw := ix1 - ix0
	ih := iy1 - iy0
	if iw <= 0 || ih <= 0 {
		return 0
	}
	inter := iw * ih
	aArea := float64(a.x1-a.x0) * float64(a.y1-a.y0)
	bArea := float64(b.x1-b.x0) * float64(b.y1-b.y0)
	return inter / (aArea + bArea - inter)
}
