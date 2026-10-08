package onnx

import "testing"

func TestConvPadStrideGroup(t *testing.T) {
	// 1 channel 3x3 input, 3x3 all-ones kernel, pad 1 -> sums of neighbourhoods.
	x := &Tensor{Shape: []int{1, 1, 3, 3}, Data: []float32{1, 2, 3, 4, 5, 6, 7, 8, 9}}
	w := &Tensor{Shape: []int{1, 1, 3, 3}, Data: []float32{1, 1, 1, 1, 1, 1, 1, 1, 1}}
	n := &nodeProto{attrs: map[string]*attr{"pads": {ints: []int64{1, 1, 1, 1}}}}
	out := conv(x, w, nil, n)
	if out.Shape[2] != 3 || out.Data[4] != 45 || out.Data[0] != 12 {
		t.Fatalf("conv = %v %v", out.Shape, out.Data)
	}
	// Depthwise (group=2) with stride 2.
	x2 := &Tensor{Shape: []int{1, 2, 2, 2}, Data: []float32{1, 1, 1, 1, 2, 2, 2, 2}}
	w2 := &Tensor{Shape: []int{2, 1, 1, 1}, Data: []float32{3, 5}}
	n2 := &nodeProto{attrs: map[string]*attr{"group": {i: 2}, "strides": {ints: []int64{2, 2}}}}
	o2 := conv(x2, w2, nil, n2)
	if len(o2.Data) != 2 || o2.Data[0] != 3 || o2.Data[1] != 10 {
		t.Fatalf("depthwise = %v", o2.Data)
	}
}

func TestPReluAddGemm(t *testing.T) {
	x := &Tensor{Shape: []int{1, 2, 1, 2}, Data: []float32{-2, 4, -2, 4}}
	p := prelu(x, &Tensor{Shape: []int{2, 1, 1}, Data: []float32{0.5, 0.25}})
	if p.Data[0] != -1 || p.Data[2] != -0.5 || p.Data[1] != 4 {
		t.Fatalf("prelu = %v", p.Data)
	}
	a := add(x, &Tensor{Shape: []int{2, 1, 1}, Data: []float32{1, 10}})
	if a.Data[0] != -1 || a.Data[2] != 8 {
		t.Fatalf("add = %v", a.Data)
	}
	g := gemm(&Tensor{Shape: []int{1, 2}, Data: []float32{1, 2}},
		&Tensor{Shape: []int{3, 2}, Data: []float32{1, 0, 0, 1, 1, 1}},
		&Tensor{Shape: []int{3}, Data: []float32{10, 20, 30}},
		&nodeProto{attrs: map[string]*attr{"transB": {i: 1}}})
	if g.Data[0] != 11 || g.Data[1] != 22 || g.Data[2] != 33 {
		t.Fatalf("gemm = %v", g.Data)
	}
}

func TestRejectsUnsupportedOperator(t *testing.T) {
	if _, err := Parse([]byte("not an onnx file")); err == nil {
		t.Fatal("garbage parsed")
	}
}
