package onnx

import (
	"math"
	"os"
	"testing"
	"time"
)

func TestRealModel(t *testing.T) {
	p := os.Getenv("ONNX_MODEL")
	if p == "" {
		t.Skip("ONNX_MODEL not set")
	}
	m, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	in := newTensor(1, 3, 112, 112)
	for i := range in.Data {
		in.Data[i] = float32(math.Sin(float64(i)*0.01)) * 0.5
	}
	s := time.Now()
	out, err := m.Run(in)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(out.Shape, time.Since(s), out.Data[:4])
}
