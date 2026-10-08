package onnx

import (
	"fmt"
	"math"
	"os"
	"runtime"
	"sync"
)

// Tensor is a dense float32 tensor in row-major (NCHW) layout.
type Tensor struct {
	Shape []int
	Data  []float32
}

func newTensor(shape ...int) *Tensor {
	n := 1
	for _, d := range shape {
		n *= d
	}
	return &Tensor{Shape: shape, Data: make([]float32, n)}
}

// Model is a loaded, validated network. Run is safe for concurrent use.
type Model struct {
	g      *graphProto
	input  string
	output string
}

var supported = map[string]bool{
	// MobileFaceNet / ArcFace operators
	"Conv": true, "PRelu": true, "Add": true, "BatchNormalization": true,
	"Flatten": true, "Gemm": true, "Relu": true,
	// SCRFD additional operators
	"MaxPool": true, "GlobalAveragePool": true, "Sigmoid": true,
	"Reshape": true, "Transpose": true, "Concat": true, "Slice": true,
	"Mul": true, "Sub": true, "Div": true, "Expand": true, "Pad": true,
	"Softmax": true, "LeakyRelu": true, "Resize": true, "Shape": true,
	"Gather": true, "Unsqueeze": true, "Squeeze": true,
}

// Load reads an ONNX file.
func Load(path string) (*Model, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(b)
}

// Parse decodes an ONNX model and rejects unsupported operators up front.
func Parse(b []byte) (m *Model, err error) {
	defer func() {
		if r := recover(); r != nil {
			m, err = nil, fmt.Errorf("onnx: malformed model: %v", r)
		}
	}()
	g, err := parseModel(b)
	if err != nil {
		return nil, err
	}
	for _, n := range g.nodes {
		if !supported[n.op] {
			return nil, fmt.Errorf("onnx: unsupported operator %q", n.op)
		}
	}
	var in []string
	for _, name := range g.inputs {
		if _, isWeight := g.inits[name]; !isWeight {
			in = append(in, name)
		}
	}
	if len(in) != 1 {
		return nil, fmt.Errorf("onnx: want 1 input, got %d", len(in))
	}
	if len(g.outputs) == 0 {
		return nil, fmt.Errorf("onnx: model has no outputs")
	}
	out := g.outputs[0]
	if len(g.outputs) > 1 {
		// Multi-output models (e.g. SCRFD): output is the empty string sentinel,
		// RunMulti is used instead of Run.
		out = ""
	}
	return &Model{g: g, input: in[0], output: out}, nil
}

// OutputCount returns the number of graph outputs declared in the model.
func (m *Model) OutputCount() int { return len(m.g.outputs) }

// Run executes the network on one input and returns its (first) output tensor.
// For multi-output models use RunMulti.
func (m *Model) Run(in *Tensor) (out *Tensor, err error) {
	outs, err := m.RunMulti(in)
	if err != nil {
		return nil, err
	}
	return outs[0], nil
}

// RunMulti executes the network on one input and returns all output tensors in
// graph-declaration order.
func (m *Model) RunMulti(in *Tensor) (out []*Tensor, err error) {
	defer func() {
		if r := recover(); r != nil {
			out, err = nil, fmt.Errorf("onnx: run failed: %v", r)
		}
	}()
	vals := map[string]*Tensor{m.input: in}
	get := func(name string) *Tensor {
		if t, ok := vals[name]; ok {
			return t
		}
		if w, ok := m.g.inits[name]; ok {
			t := &Tensor{Shape: w.dims, Data: w.f32}
			vals[name] = t
			return t
		}
		panic("missing value " + name)
	}
	for _, n := range m.g.nodes {
		var r *Tensor
		switch n.op {
		case "Conv":
			var bias *Tensor
			if len(n.inputs) > 2 {
				bias = get(n.inputs[2])
			}
			r = conv(get(n.inputs[0]), get(n.inputs[1]), bias, n)
		case "PRelu":
			r = prelu(get(n.inputs[0]), get(n.inputs[1]))
		case "Relu":
			r = relu(get(n.inputs[0]))
		case "LeakyRelu":
			r = leakyRelu(get(n.inputs[0]), n)
		case "Add":
			r = add(get(n.inputs[0]), get(n.inputs[1]))
		case "Sub":
			r = sub(get(n.inputs[0]), get(n.inputs[1]))
		case "Mul":
			r = mul(get(n.inputs[0]), get(n.inputs[1]))
		case "Div":
			r = div(get(n.inputs[0]), get(n.inputs[1]))
		case "BatchNormalization":
			r = batchNorm(get(n.inputs[0]), get(n.inputs[1]), get(n.inputs[2]),
				get(n.inputs[3]), get(n.inputs[4]), n)
		case "Flatten":
			r = flatten(get(n.inputs[0]), n)
		case "Gemm":
			var c *Tensor
			if len(n.inputs) > 2 {
				c = get(n.inputs[2])
			}
			r = gemm(get(n.inputs[0]), get(n.inputs[1]), c, n)
		case "MaxPool":
			r = maxpool(get(n.inputs[0]), n)
		case "GlobalAveragePool":
			r = globalAvgPool(get(n.inputs[0]))
		case "Sigmoid":
			r = sigmoid(get(n.inputs[0]))
		case "Softmax":
			r = softmax(get(n.inputs[0]), n)
		case "Reshape":
			r = reshape(get(n.inputs[0]), get(n.inputs[1]))
		case "Transpose":
			r = transpose(get(n.inputs[0]), n)
		case "Concat":
			inputs := make([]*Tensor, len(n.inputs))
			for i, inp := range n.inputs {
				inputs[i] = get(inp)
			}
			r = concat(inputs, n)
		case "Slice":
			var starts, ends, axes, steps *Tensor
			starts = get(n.inputs[1])
			ends = get(n.inputs[2])
			if len(n.inputs) > 3 {
				axes = get(n.inputs[3])
			}
			if len(n.inputs) > 4 {
				steps = get(n.inputs[4])
			}
			r = sliceTensor(get(n.inputs[0]), starts, ends, axes, steps)
		case "Expand":
			r = expand(get(n.inputs[0]), get(n.inputs[1]))
		case "Pad":
			pads := get(n.inputs[1])
			var cv *Tensor
			if len(n.inputs) > 2 {
				cv = get(n.inputs[2])
			}
			r = pad(get(n.inputs[0]), pads, cv, n)
		case "Resize":
			// Approximate Resize: nearest-neighbour or bilinear.
			// inputs: X, roi (optional), scales (optional), sizes (optional)
			var sizes *Tensor
			if len(n.inputs) > 3 {
				sizes = get(n.inputs[3])
			} else if len(n.inputs) > 2 {
				sizes = get(n.inputs[2])
			}
			r = resizeTensor(get(n.inputs[0]), sizes, n)
		case "Shape":
			r = shapeTensor(get(n.inputs[0]))
		case "Gather":
			r = gather(get(n.inputs[0]), get(n.inputs[1]), n)
		case "Unsqueeze":
			axes := n.attrs["axes"]
			if len(n.inputs) > 1 {
				r = unsqueeze(get(n.inputs[0]), get(n.inputs[1]))
			} else {
				r = unsqueezeAttrs(get(n.inputs[0]), axes)
			}
		case "Squeeze":
			var ax *Tensor
			if len(n.inputs) > 1 {
				ax = get(n.inputs[1])
			}
			r = squeezeTensor(get(n.inputs[0]), ax, n)
		default:
			panic("unsupported op: " + n.op)
		}
		if len(n.outputs) == 1 {
			vals[n.outputs[0]] = r
		} else {
			// Some ops like MaxPool may have multiple outputs (result + mask).
			// We only compute the first.
			vals[n.outputs[0]] = r
		}
	}
	result := make([]*Tensor, len(m.g.outputs))
	for i, name := range m.g.outputs {
		t, ok := vals[name]
		if !ok {
			return nil, fmt.Errorf("onnx: output %q was not produced", name)
		}
		result[i] = t
	}
	return result, nil
}

func (a *attr) intsOr(def ...int) []int {
	if a == nil || len(a.ints) == 0 {
		return def
	}
	out := make([]int, len(a.ints))
	for i, v := range a.ints {
		out[i] = int(v)
	}
	return out
}

func parallel(n int, fn func(i int)) {
	w := runtime.GOMAXPROCS(0)
	if w > n {
		w = n
	}
	var wg sync.WaitGroup
	next := make(chan int, n)
	for i := 0; i < n; i++ {
		next <- i
	}
	close(next)
	for k := 0; k < w; k++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				fn(i)
			}
		}()
	}
	wg.Wait()
}

func conv(x, w, bias *Tensor, n *nodeProto) *Tensor {
	N, C, H, W := x.Shape[0], x.Shape[1], x.Shape[2], x.Shape[3]
	M, cg, kh, kw := w.Shape[0], w.Shape[1], w.Shape[2], w.Shape[3]
	group := 1
	if a := n.attrs["group"]; a != nil {
		group = int(a.i)
	}
	st := n.attrs["strides"].intsOr(1, 1)
	pad := n.attrs["pads"].intsOr(0, 0, 0, 0)
	dil := n.attrs["dilations"].intsOr(1, 1)
	if cg*group != C || M%group != 0 {
		panic("conv: channel/group mismatch")
	}
	oh := (H+pad[0]+pad[2]-dil[0]*(kh-1)-1)/st[0] + 1
	ow := (W+pad[1]+pad[3]-dil[1]*(kw-1)-1)/st[1] + 1
	out := newTensor(N, M, oh, ow)
	mg := M / group
	for b := 0; b < N; b++ {
		parallel(M, func(m int) {
			g := m / mg
			dst := out.Data[(b*M+m)*oh*ow : (b*M+m+1)*oh*ow]
			if bias != nil {
				for i := range dst {
					dst[i] = bias.Data[m]
				}
			}
			for c := 0; c < cg; c++ {
				src := x.Data[(b*C+g*cg+c)*H*W : (b*C+g*cg+c+1)*H*W]
				wk := w.Data[((m*cg+c)*kh)*kw : ((m*cg+c)*kh+kh)*kw]
				for ky := 0; ky < kh; ky++ {
					for kx := 0; kx < kw; kx++ {
						wv := wk[ky*kw+kx]
						if wv == 0 {
							continue
						}
						for y := 0; y < oh; y++ {
							iy := y*st[0] - pad[0] + ky*dil[0]
							if iy < 0 || iy >= H {
								continue
							}
							row := src[iy*W : (iy+1)*W]
							drow := dst[y*ow : (y+1)*ow]
							ix := -pad[1] + kx*dil[1]
							for xx := 0; xx < ow; xx++ {
								if ix >= 0 && ix < W {
									drow[xx] += wv * row[ix]
								}
								ix += st[1]
							}
						}
					}
				}
			}
		})
	}
	return out
}

func prelu(x, slope *Tensor) *Tensor {
	out := newTensor(x.Shape...)
	per := 1 // elements sharing one slope
	if len(slope.Data) > 1 && len(x.Shape) == 4 {
		per = x.Shape[2] * x.Shape[3]
	}
	for i, v := range x.Data {
		if v < 0 {
			s := slope.Data[0]
			if len(slope.Data) > 1 {
				s = slope.Data[(i/per)%len(slope.Data)]
			}
			v *= s
		}
		out.Data[i] = v
	}
	return out
}

func relu(x *Tensor) *Tensor {
	out := newTensor(x.Shape...)
	for i, v := range x.Data {
		if v > 0 {
			out.Data[i] = v
		}
	}
	return out
}

func add(a, b *Tensor) *Tensor {
	if len(a.Data) < len(b.Data) {
		a, b = b, a
	}
	out := newTensor(a.Shape...)
	switch {
	case len(a.Data) == len(b.Data):
		for i := range a.Data {
			out.Data[i] = a.Data[i] + b.Data[i]
		}
	case len(a.Shape) == 4 && len(b.Data) == a.Shape[1]:
		per := a.Shape[2] * a.Shape[3]
		for i := range a.Data {
			out.Data[i] = a.Data[i] + b.Data[(i/per)%a.Shape[1]]
		}
	case len(b.Data) == 1:
		for i := range a.Data {
			out.Data[i] = a.Data[i] + b.Data[0]
		}
	default:
		panic("add: unsupported broadcast")
	}
	return out
}

func batchNorm(x, scale, bias, mean, variance *Tensor, n *nodeProto) *Tensor {
	eps := float32(1e-5)
	if a := n.attrs["epsilon"]; a != nil {
		eps = a.f
	}
	C := x.Shape[1]
	per := len(x.Data) / (x.Shape[0] * C)
	out := newTensor(x.Shape...)
	for i, v := range x.Data {
		c := (i / per) % C
		inv := scale.Data[c] / float32(math.Sqrt(float64(variance.Data[c]+eps)))
		out.Data[i] = (v-mean.Data[c])*inv + bias.Data[c]
	}
	return out
}

func flatten(x *Tensor, n *nodeProto) *Tensor {
	axis := 1
	if a := n.attrs["axis"]; a != nil {
		axis = int(a.i)
	}
	rows := 1
	for _, d := range x.Shape[:axis] {
		rows *= d
	}
	return &Tensor{Shape: []int{rows, len(x.Data) / rows}, Data: x.Data}
}

func gemm(a, b, c *Tensor, n *nodeProto) *Tensor {
	alpha, beta := float32(1), float32(1)
	var transA, transB bool
	if x := n.attrs["alpha"]; x != nil {
		alpha = x.f
	}
	if x := n.attrs["beta"]; x != nil {
		beta = x.f
	}
	if x := n.attrs["transA"]; x != nil {
		transA = x.i != 0
	}
	if x := n.attrs["transB"]; x != nil {
		transB = x.i != 0
	}
	M, K := a.Shape[0], a.Shape[1]
	if transA {
		M, K = K, M
	}
	N := b.Shape[1]
	if transB {
		N = b.Shape[0]
	}
	out := newTensor(M, N)
	at := func(i, k int) float32 {
		if transA {
			return a.Data[k*M+i]
		}
		return a.Data[i*K+k]
	}
	bt := func(k, j int) float32 {
		if transB {
			return b.Data[j*K+k]
		}
		return b.Data[k*N+j]
	}
	for i := 0; i < M; i++ {
		for j := 0; j < N; j++ {
			var s float32
			for k := 0; k < K; k++ {
				s += at(i, k) * bt(k, j)
			}
			s *= alpha
			if c != nil {
				switch {
				case len(c.Data) == N:
					s += beta * c.Data[j]
				case len(c.Data) == M*N:
					s += beta * c.Data[i*N+j]
				default:
					s += beta * c.Data[0]
				}
			}
			out.Data[i*N+j] = s
		}
	}
	return out
}

// ── New operators for SCRFD ──────────────────────────────────────────────────

func maxpool(x *Tensor, n *nodeProto) *Tensor {
	kh, kw := 2, 2
	sh, sw := 1, 1
	ph, pw := 0, 0
	if k := n.attrs["kernel_shape"]; k != nil && len(k.ints) >= 2 {
		kh, kw = int(k.ints[0]), int(k.ints[1])
	}
	if s := n.attrs["strides"]; s != nil && len(s.ints) >= 2 {
		sh, sw = int(s.ints[0]), int(s.ints[1])
	}
	if p := n.attrs["pads"]; p != nil && len(p.ints) >= 4 {
		ph, pw = int(p.ints[0]), int(p.ints[1])
	}
	N, C, H, W := x.Shape[0], x.Shape[1], x.Shape[2], x.Shape[3]
	oh := (H+2*ph-kh)/sh + 1
	ow := (W+2*pw-kw)/sw + 1
	out := newTensor(N, C, oh, ow)
	for b := 0; b < N; b++ {
		for c := 0; c < C; c++ {
			for y := 0; y < oh; y++ {
				for xx := 0; xx < ow; xx++ {
					best := float32(math.Inf(-1))
					for ky := 0; ky < kh; ky++ {
						for kx := 0; kx < kw; kx++ {
							iy := y*sh - ph + ky
							ix := xx*sw - pw + kx
							if iy < 0 || iy >= H || ix < 0 || ix >= W {
								continue
							}
							v := x.Data[((b*C+c)*H+iy)*W+ix]
							if v > best {
								best = v
							}
						}
					}
					out.Data[((b*C+c)*oh+y)*ow+xx] = best
				}
			}
		}
	}
	return out
}

func globalAvgPool(x *Tensor) *Tensor {
	N, C, H, W := x.Shape[0], x.Shape[1], x.Shape[2], x.Shape[3]
	out := newTensor(N, C, 1, 1)
	hw := float32(H * W)
	for b := 0; b < N; b++ {
		for c := 0; c < C; c++ {
			var s float32
			base := (b*C + c) * H * W
			for i := 0; i < H*W; i++ {
				s += x.Data[base+i]
			}
			out.Data[b*C+c] = s / hw
		}
	}
	return out
}

func sigmoid(x *Tensor) *Tensor {
	out := newTensor(x.Shape...)
	for i, v := range x.Data {
		out.Data[i] = float32(1.0 / (1.0 + math.Exp(-float64(v))))
	}
	return out
}

func softmax(x *Tensor, n *nodeProto) *Tensor {
	axis := -1
	if a := n.attrs["axis"]; a != nil {
		axis = int(a.i)
	}
	out := newTensor(x.Shape...)
	rank := len(x.Shape)
	if axis < 0 {
		axis += rank
	}
	// stride across the axis dimension
	outerSize := 1
	for i := 0; i < axis; i++ {
		outerSize *= x.Shape[i]
	}
	axisSize := x.Shape[axis]
	innerSize := 1
	for i := axis + 1; i < rank; i++ {
		innerSize *= x.Shape[i]
	}
	for o := 0; o < outerSize; o++ {
		for i := 0; i < innerSize; i++ {
			base := o*axisSize*innerSize + i
			maxV := float32(math.Inf(-1))
			for k := 0; k < axisSize; k++ {
				if v := x.Data[base+k*innerSize]; v > maxV {
					maxV = v
				}
			}
			var sum float32
			for k := 0; k < axisSize; k++ {
				e := float32(math.Exp(float64(x.Data[base+k*innerSize] - maxV)))
				out.Data[base+k*innerSize] = e
				sum += e
			}
			for k := 0; k < axisSize; k++ {
				out.Data[base+k*innerSize] /= sum
			}
		}
	}
	return out
}

func leakyRelu(x *Tensor, n *nodeProto) *Tensor {
	alpha := float32(0.01)
	if a := n.attrs["alpha"]; a != nil {
		alpha = a.f
	}
	out := newTensor(x.Shape...)
	for i, v := range x.Data {
		if v < 0 {
			out.Data[i] = v * alpha
		} else {
			out.Data[i] = v
		}
	}
	return out
}

func sub(a, b *Tensor) *Tensor {
	// a is always the larger operand here; if b is bigger this panics, which is correct.
	out := newTensor(a.Shape...)
	switch {
	case len(a.Data) == len(b.Data):
		for i := range a.Data {
			out.Data[i] = a.Data[i] - b.Data[i]
		}
	case len(a.Shape) == 4 && len(b.Data) == a.Shape[1]:
		per := a.Shape[2] * a.Shape[3]
		for i := range a.Data {
			out.Data[i] = a.Data[i] - b.Data[(i/per)%a.Shape[1]]
		}
	case len(b.Data) == 1:
		for i := range a.Data {
			out.Data[i] = a.Data[i] - b.Data[0]
		}
	default:
		panic("sub: unsupported broadcast")
	}
	return out
}

func mul(a, b *Tensor) *Tensor {
	if len(a.Data) < len(b.Data) {
		a, b = b, a
	}
	out := newTensor(a.Shape...)
	switch {
	case len(a.Data) == len(b.Data):
		for i := range a.Data {
			out.Data[i] = a.Data[i] * b.Data[i]
		}
	case len(a.Shape) == 4 && len(b.Data) == a.Shape[1]:
		per := a.Shape[2] * a.Shape[3]
		for i := range a.Data {
			out.Data[i] = a.Data[i] * b.Data[(i/per)%a.Shape[1]]
		}
	case len(b.Data) == 1:
		for i := range a.Data {
			out.Data[i] = a.Data[i] * b.Data[0]
		}
	default:
		panic("mul: unsupported broadcast")
	}
	return out
}

func div(a, b *Tensor) *Tensor {
	if len(a.Data) < len(b.Data) {
		a, b = b, a
	}
	out := newTensor(a.Shape...)
	switch {
	case len(a.Data) == len(b.Data):
		for i := range a.Data {
			out.Data[i] = a.Data[i] / b.Data[i]
		}
	case len(b.Data) == 1:
		for i := range a.Data {
			out.Data[i] = a.Data[i] / b.Data[0]
		}
	default:
		panic("div: unsupported broadcast")
	}
	return out
}

// reshape reads the target shape from a int64 tensor stored as float32.
func reshape(x, shape *Tensor) *Tensor {
	dims := make([]int, len(shape.Data))
	total := 1
	neg := -1
	for i, v := range shape.Data {
		d := int(v)
		if d == 0 {
			// 0 means "keep the same dimension as input"
			if i < len(x.Shape) {
				d = x.Shape[i]
			} else {
				d = 1
			}
		}
		if d == -1 {
			neg = i
			dims[i] = -1
		} else {
			dims[i] = d
			total *= d
		}
	}
	if neg >= 0 {
		dims[neg] = len(x.Data) / total
	}
	return &Tensor{Shape: dims, Data: x.Data}
}

func transpose(x *Tensor, n *nodeProto) *Tensor {
	perm := n.attrs["perm"]
	rank := len(x.Shape)
	p := make([]int, rank)
	if perm != nil && len(perm.ints) == rank {
		for i, v := range perm.ints {
			p[i] = int(v)
		}
	} else {
		// Default: reverse
		for i := range p {
			p[i] = rank - 1 - i
		}
	}
	newShape := make([]int, rank)
	for i, pi := range p {
		newShape[i] = x.Shape[pi]
	}
	out := newTensor(newShape...)
	// compute strides for input
	inStrides := make([]int, rank)
	inStrides[rank-1] = 1
	for i := rank - 2; i >= 0; i-- {
		inStrides[i] = inStrides[i+1] * x.Shape[i+1]
	}
	outStrides := make([]int, rank)
	outStrides[rank-1] = 1
	for i := rank - 2; i >= 0; i-- {
		outStrides[i] = outStrides[i+1] * out.Shape[i+1]
	}
	n1 := len(x.Data)
	for i := 0; i < n1; i++ {
		// decompose i into input indices
		rem := i
		srcIdx := 0
		dstIdx := 0
		for dim := rank - 1; dim >= 0; dim-- {
			coord := rem % x.Shape[dim]
			rem /= x.Shape[dim]
			srcIdx += coord * inStrides[dim]
			// find where this dimension maps in output
			for outDim, inDim := range p {
				if inDim == dim {
					dstIdx += coord * outStrides[outDim]
					break
				}
			}
		}
		out.Data[srcIdx] = x.Data[dstIdx]
	}
	// The loop above is wrong; use direct index computation instead.
	// Reset and redo correctly.
	for i := range out.Data {
		out.Data[i] = 0
	}
	cur := make([]int, rank)
	for i := 0; i < n1; i++ {
		// compute multi-index in out
		rem := i
		for d := rank - 1; d >= 0; d-- {
			cur[d] = rem % newShape[d]
			rem /= newShape[d]
		}
		// map back to input index
		srcFlat := 0
		for d, outDim := range cur {
			srcFlat += outDim * inStrides[p[d]]
		}
		out.Data[i] = x.Data[srcFlat]
	}
	return out
}

func concat(inputs []*Tensor, n *nodeProto) *Tensor {
	axis := 0
	if a := n.attrs["axis"]; a != nil {
		axis = int(a.i)
	}
	rank := len(inputs[0].Shape)
	if axis < 0 {
		axis += rank
	}
	newShape := make([]int, rank)
	copy(newShape, inputs[0].Shape)
	for _, t := range inputs[1:] {
		newShape[axis] += t.Shape[axis]
	}
	out := newTensor(newShape...)

	// Strides for the output tensor
	outStrides := make([]int, rank)
	outStrides[rank-1] = 1
	for i := rank - 2; i >= 0; i-- {
		outStrides[i] = outStrides[i+1] * newShape[i+1]
	}

	offset := 0
	for _, t := range inputs {
		// stride for this input
		inStrides := make([]int, rank)
		inStrides[rank-1] = 1
		for i := rank - 2; i >= 0; i-- {
			inStrides[i] = inStrides[i+1] * t.Shape[i+1]
		}
		for i := range t.Data {
			// decompose flat index into coords in t
			rem := i
			coords := make([]int, rank)
			for d := rank - 1; d >= 0; d-- {
				coords[d] = rem % t.Shape[d]
				rem /= t.Shape[d]
			}
			// adjust concat axis by offset
			coords[axis] += offset
			dstFlat := 0
			for d, c := range coords {
				dstFlat += c * outStrides[d]
			}
			out.Data[dstFlat] = t.Data[i]
		}
		offset += t.Shape[axis]
	}
	return out
}

func sliceTensor(x, starts, ends, axes, steps *Tensor) *Tensor {
	rank := len(x.Shape)
	// Build per-axis start/end/step
	st := make([]int, rank)
	en := make([]int, rank)
	sp := make([]int, rank)
	for i := range st {
		st[i] = 0
		en[i] = x.Shape[i]
		sp[i] = 1
	}
	nSlices := len(starts.Data)
	for s := 0; s < nSlices; s++ {
		ax := s
		if axes != nil {
			ax = int(axes.Data[s])
			if ax < 0 {
				ax += rank
			}
		}
		step := 1
		if steps != nil {
			step = int(steps.Data[s])
		}
		start := int(starts.Data[s])
		end := int(ends.Data[s])
		dim := x.Shape[ax]
		// clamp large values (ONNX uses INT_MAX for "to the end")
		if start > dim {
			start = dim
		}
		if start < -dim {
			start = -dim
		}
		if start < 0 {
			start += dim
		}
		if end > dim {
			end = dim
		}
		if end < -dim {
			end = 0
		}
		if end < 0 {
			end += dim
		}
		st[ax] = start
		en[ax] = end
		sp[ax] = step
	}
	// Compute output shape
	outShape := make([]int, rank)
	for d := 0; d < rank; d++ {
		sz := (en[d] - st[d] + sp[d] - 1) / sp[d]
		if sz < 0 {
			sz = 0
		}
		outShape[d] = sz
	}
	out := newTensor(outShape...)
	// Strides for x
	inStrides := make([]int, rank)
	inStrides[rank-1] = 1
	for i := rank - 2; i >= 0; i-- {
		inStrides[i] = inStrides[i+1] * x.Shape[i+1]
	}
	total := 1
	for _, d := range outShape {
		total *= d
	}
	for i := 0; i < total; i++ {
		rem := i
		srcFlat := 0
		for d := rank - 1; d >= 0; d-- {
			c := rem % outShape[d]
			rem /= outShape[d]
			srcFlat += (st[d] + c*sp[d]) * inStrides[d]
		}
		out.Data[i] = x.Data[srcFlat]
	}
	return out
}

func expand(x, shape *Tensor) *Tensor {
	// shape is a 1-D int64 tensor stored as float32
	targetShape := make([]int, len(shape.Data))
	for i, v := range shape.Data {
		targetShape[i] = int(v)
	}
	rank := len(targetShape)
	// pad x.Shape on the left if needed
	xShape := x.Shape
	for len(xShape) < rank {
		xShape = append([]int{1}, xShape...)
	}
	outShape := make([]int, rank)
	for i := range outShape {
		a, b := xShape[i], targetShape[i]
		if a > b {
			outShape[i] = a
		} else {
			outShape[i] = b
		}
	}
	out := newTensor(outShape...)
	inStrides := make([]int, rank)
	inStrides[rank-1] = 1
	for i := rank - 2; i >= 0; i-- {
		inStrides[i] = inStrides[i+1] * xShape[i+1]
	}
	for i := range out.Data {
		rem := i
		srcFlat := 0
		for d := rank - 1; d >= 0; d-- {
			c := rem % outShape[d]
			rem /= outShape[d]
			ic := c % xShape[d] // broadcast
			srcFlat += ic * inStrides[d]
		}
		out.Data[i] = x.Data[srcFlat]
	}
	return out
}

func pad(x, pads, cv *Tensor, n *nodeProto) *Tensor {
	// Constant padding only (mode = constant).
	rank := len(x.Shape)
	padVals := make([]int, 2*rank)
	for i, v := range pads.Data {
		if i < 2*rank {
			padVals[i] = int(v)
		}
	}
	var cVal float32
	if cv != nil && len(cv.Data) > 0 {
		cVal = cv.Data[0]
	}
	newShape := make([]int, rank)
	for d := 0; d < rank; d++ {
		newShape[d] = x.Shape[d] + padVals[d] + padVals[rank+d]
	}
	out := newTensor(newShape...)
	if cVal != 0 {
		for i := range out.Data {
			out.Data[i] = cVal
		}
	}
	inStrides := make([]int, rank)
	inStrides[rank-1] = 1
	for i := rank - 2; i >= 0; i-- {
		inStrides[i] = inStrides[i+1] * x.Shape[i+1]
	}
	outStrides := make([]int, rank)
	outStrides[rank-1] = 1
	for i := rank - 2; i >= 0; i-- {
		outStrides[i] = outStrides[i+1] * newShape[i+1]
	}
	total := len(x.Data)
	for i := 0; i < total; i++ {
		rem := i
		dstFlat := 0
		for d := rank - 1; d >= 0; d-- {
			c := rem % x.Shape[d]
			rem /= x.Shape[d]
			dstFlat += (c + padVals[d]) * outStrides[d]
		}
		out.Data[dstFlat] = x.Data[i]
	}
	return out
}

func resizeTensor(x, sizes *Tensor, n *nodeProto) *Tensor {
	// Support 4-D only (batch, channel, H, W).
	if len(x.Shape) != 4 {
		panic("resize: only 4-D tensors supported")
	}
	N, C, H, W := x.Shape[0], x.Shape[1], x.Shape[2], x.Shape[3]
	var oh, ow int
	if sizes != nil && len(sizes.Data) >= 4 {
		oh = int(sizes.Data[2])
		ow = int(sizes.Data[3])
	} else {
		oh, ow = H, W
	}
	out := newTensor(N, C, oh, ow)
	scaleH := float64(H) / float64(oh)
	scaleW := float64(W) / float64(ow)
	for b := 0; b < N; b++ {
		for c := 0; c < C; c++ {
			for y := 0; y < oh; y++ {
				for xx := 0; xx < ow; xx++ {
					sy := int(float64(y)*scaleH + 0.5)
					sx := int(float64(xx)*scaleW + 0.5)
					if sy >= H {
						sy = H - 1
					}
					if sx >= W {
						sx = W - 1
					}
					out.Data[((b*C+c)*oh+y)*ow+xx] = x.Data[((b*C+c)*H+sy)*W+sx]
				}
			}
		}
	}
	return out
}

func shapeTensor(x *Tensor) *Tensor {
	out := &Tensor{Shape: []int{len(x.Shape)}, Data: make([]float32, len(x.Shape))}
	for i, d := range x.Shape {
		out.Data[i] = float32(d)
	}
	return out
}

func gather(x, indices *Tensor, n *nodeProto) *Tensor {
	axis := 0
	if a := n.attrs["axis"]; a != nil {
		axis = int(a.i)
	}
	rank := len(x.Shape)
	if axis < 0 {
		axis += rank
	}
	// indices shape + output shape
	idxLen := len(indices.Data)
	// Output shape: x.Shape[:axis] + indices.Shape + x.Shape[axis+1:]
	outShape := make([]int, 0, rank-1+len(indices.Shape))
	outShape = append(outShape, x.Shape[:axis]...)
	outShape = append(outShape, indices.Shape...)
	outShape = append(outShape, x.Shape[axis+1:]...)
	if len(outShape) == 0 {
		outShape = []int{1}
	}
	out := newTensor(outShape...)

	innerSize := 1
	for d := axis + 1; d < rank; d++ {
		innerSize *= x.Shape[d]
	}
	outerSize := 1
	for d := 0; d < axis; d++ {
		outerSize *= x.Shape[d]
	}
	axisSize := x.Shape[axis]

	for o := 0; o < outerSize; o++ {
		for i := 0; i < idxLen; i++ {
			idx := int(indices.Data[i])
			if idx < 0 {
				idx += axisSize
			}
			srcBase := (o*axisSize + idx) * innerSize
			dstBase := (o*idxLen + i) * innerSize
			copy(out.Data[dstBase:dstBase+innerSize], x.Data[srcBase:srcBase+innerSize])
		}
	}
	return out
}

func unsqueeze(x, axes *Tensor) *Tensor {
	return unsqueezeAttrs(x, &attr{ints: func() []int64 {
		out := make([]int64, len(axes.Data))
		for i, v := range axes.Data {
			out[i] = int64(v)
		}
		return out
	}()})
}

func unsqueezeAttrs(x *Tensor, axes *attr) *Tensor {
	rank := len(x.Shape)
	var axList []int
	if axes != nil {
		for _, a := range axes.ints {
			ax := int(a)
			if ax < 0 {
				ax += rank + len(axes.ints)
			}
			axList = append(axList, ax)
		}
	}
	newRank := rank + len(axList)
	newShape := make([]int, newRank)
	for i := range newShape {
		newShape[i] = 1
	}
	axSet := map[int]bool{}
	for _, a := range axList {
		axSet[a] = true
	}
	j := 0
	for i := 0; i < newRank; i++ {
		if !axSet[i] {
			newShape[i] = x.Shape[j]
			j++
		}
	}
	return &Tensor{Shape: newShape, Data: x.Data}
}

func squeezeTensor(x, axesTensor *Tensor, n *nodeProto) *Tensor {
	rank := len(x.Shape)
	axSet := map[int]bool{}
	if axesTensor != nil {
		for _, v := range axesTensor.Data {
			ax := int(v)
			if ax < 0 {
				ax += rank
			}
			axSet[ax] = true
		}
	} else if a := n.attrs["axes"]; a != nil {
		for _, v := range a.ints {
			ax := int(v)
			if ax < 0 {
				ax += rank
			}
			axSet[ax] = true
		}
	} else {
		// squeeze all 1-dims
		for i, d := range x.Shape {
			if d == 1 {
				axSet[i] = true
			}
		}
	}
	newShape := make([]int, 0, rank)
	for i, d := range x.Shape {
		if !axSet[i] {
			newShape = append(newShape, d)
		}
	}
	if len(newShape) == 0 {
		newShape = []int{1}
	}
	return &Tensor{Shape: newShape, Data: x.Data}
}
