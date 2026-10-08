// Package onnx is a small pure-Go inference engine for convolutional ONNX
// models (the face-embedding networks Cairn uses). It implements only the
// operators those networks need and rejects anything else when the model is
// loaded, so an unsupported file fails early instead of producing garbage.
package onnx

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// The ONNX file is a protobuf; this is the minimal wire-format reader for the
// handful of messages the engine needs (ModelProto, GraphProto, NodeProto,
// AttributeProto, TensorProto).

var errTruncated = errors.New("onnx: truncated protobuf")

type pbField struct {
	num  int
	wire int
	val  uint64 // varint, or fixed32/fixed64 bits
	data []byte // length-delimited payload
}

// pbFields splits one message into its fields.
func pbFields(b []byte) ([]pbField, error) {
	var out []pbField
	for len(b) > 0 {
		key, n := binary.Uvarint(b)
		if n <= 0 {
			return nil, errTruncated
		}
		b = b[n:]
		f := pbField{num: int(key >> 3), wire: int(key & 7)}
		switch f.wire {
		case 0:
			v, n := binary.Uvarint(b)
			if n <= 0 {
				return nil, errTruncated
			}
			f.val, b = v, b[n:]
		case 1:
			if len(b) < 8 {
				return nil, errTruncated
			}
			f.val, b = binary.LittleEndian.Uint64(b), b[8:]
		case 2:
			l, n := binary.Uvarint(b)
			if n <= 0 || uint64(len(b)-n) < l {
				return nil, errTruncated
			}
			f.data, b = b[n:n+int(l)], b[n+int(l):]
		case 5:
			if len(b) < 4 {
				return nil, errTruncated
			}
			f.val, b = uint64(binary.LittleEndian.Uint32(b)), b[4:]
		default:
			return nil, fmt.Errorf("onnx: unsupported protobuf wire type %d", f.wire)
		}
		out = append(out, f)
	}
	return out, nil
}

// packedVarints decodes a packed repeated varint field.
func packedVarints(b []byte) ([]int64, error) {
	var out []int64
	for len(b) > 0 {
		v, n := binary.Uvarint(b)
		if n <= 0 {
			return nil, errTruncated
		}
		out = append(out, int64(v))
		b = b[n:]
	}
	return out, nil
}

type tensorProto struct {
	name string
	dims []int
	dt   int
	f32  []float32
}

const (
	dtFloat = 1
	dtInt64 = 7
)

func parseTensor(b []byte) (*tensorProto, error) {
	fs, err := pbFields(b)
	if err != nil {
		return nil, err
	}
	t := &tensorProto{}
	var raw []byte
	var i64 []int64
	for _, f := range fs {
		switch f.num {
		case 1:
			if f.wire == 2 {
				ds, err := packedVarints(f.data)
				if err != nil {
					return nil, err
				}
				for _, d := range ds {
					t.dims = append(t.dims, int(d))
				}
			} else {
				t.dims = append(t.dims, int(f.val))
			}
		case 2:
			t.dt = int(f.val)
		case 4: // float_data
			if f.wire == 2 {
				for i := 0; i+4 <= len(f.data); i += 4 {
					t.f32 = append(t.f32, math.Float32frombits(binary.LittleEndian.Uint32(f.data[i:])))
				}
			} else {
				t.f32 = append(t.f32, math.Float32frombits(uint32(f.val)))
			}
		case 7: // int64_data
			if f.wire == 2 {
				vs, err := packedVarints(f.data)
				if err != nil {
					return nil, err
				}
				i64 = append(i64, vs...)
			} else {
				i64 = append(i64, int64(f.val))
			}
		case 8:
			t.name = string(f.data)
		case 9:
			raw = f.data
		}
	}
	switch t.dt {
	case dtFloat:
		if raw != nil {
			if len(raw)%4 != 0 {
				return nil, errTruncated
			}
			t.f32 = make([]float32, len(raw)/4)
			for i := range t.f32 {
				t.f32[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4:]))
			}
		}
	case dtInt64: // shapes and axes: kept as float32, they are tiny
		if raw != nil {
			for i := 0; i+8 <= len(raw); i += 8 {
				i64 = append(i64, int64(binary.LittleEndian.Uint64(raw[i:])))
			}
		}
		t.f32 = make([]float32, len(i64))
		for i, v := range i64 {
			t.f32[i] = float32(v)
		}
	default:
		return nil, fmt.Errorf("onnx: unsupported tensor type %d", t.dt)
	}
	return t, nil
}

type attr struct {
	f    float32
	i    int64
	ints []int64
	t    *tensorProto
}

type nodeProto struct {
	op      string
	name    string
	inputs  []string
	outputs []string
	attrs   map[string]*attr
}

func parseNode(b []byte) (*nodeProto, error) {
	fs, err := pbFields(b)
	if err != nil {
		return nil, err
	}
	n := &nodeProto{attrs: map[string]*attr{}}
	for _, f := range fs {
		switch f.num {
		case 1:
			n.inputs = append(n.inputs, string(f.data))
		case 2:
			n.outputs = append(n.outputs, string(f.data))
		case 3:
			n.name = string(f.data)
		case 4:
			n.op = string(f.data)
		case 5:
			name, a, err := parseAttr(f.data)
			if err != nil {
				return nil, err
			}
			n.attrs[name] = a
		}
	}
	return n, nil
}

func parseAttr(b []byte) (string, *attr, error) {
	fs, err := pbFields(b)
	if err != nil {
		return "", nil, err
	}
	a := &attr{}
	var name string
	for _, f := range fs {
		switch f.num {
		case 1:
			name = string(f.data)
		case 2:
			a.f = math.Float32frombits(uint32(f.val))
		case 3:
			a.i = int64(f.val)
		case 5:
			if a.t, err = parseTensor(f.data); err != nil {
				return "", nil, err
			}
		case 8:
			if f.wire == 2 {
				vs, err := packedVarints(f.data)
				if err != nil {
					return "", nil, err
				}
				a.ints = append(a.ints, vs...)
			} else {
				a.ints = append(a.ints, int64(f.val))
			}
		}
	}
	return name, a, nil
}

type graphProto struct {
	nodes   []*nodeProto
	inits   map[string]*tensorProto
	inputs  []string
	outputs []string
}

func parseValueName(b []byte) string {
	fs, err := pbFields(b)
	if err != nil {
		return ""
	}
	for _, f := range fs {
		if f.num == 1 {
			return string(f.data)
		}
	}
	return ""
}

func parseModel(b []byte) (*graphProto, error) {
	fs, err := pbFields(b)
	if err != nil {
		return nil, err
	}
	for _, f := range fs {
		if f.num != 7 {
			continue
		}
		gf, err := pbFields(f.data)
		if err != nil {
			return nil, err
		}
		g := &graphProto{inits: map[string]*tensorProto{}}
		for _, x := range gf {
			switch x.num {
			case 1:
				n, err := parseNode(x.data)
				if err != nil {
					return nil, err
				}
				g.nodes = append(g.nodes, n)
			case 5:
				t, err := parseTensor(x.data)
				if err != nil {
					return nil, err
				}
				g.inits[t.name] = t
			case 11:
				g.inputs = append(g.inputs, parseValueName(x.data))
			case 12:
				g.outputs = append(g.outputs, parseValueName(x.data))
			}
		}
		return g, nil
	}
	return nil, errors.New("onnx: no graph in model")
}
