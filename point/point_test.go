package point

import (
	"bytes"
	"errors"
	"math"
	"testing"
)

func TestPointRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		p    Point
	}{
		{"zero", Point{0, 0}},
		{"positive", Point{123456789, 3.5}},
		{"negative ts", Point{-1, -2.25}},
		{"min ts", Point{math.MinInt64, -1e300}},
		{"max ts", Point{math.MaxInt64, 1e300}},
		{"plus inf", Point{7, math.Inf(1)}},
		{"minus inf", Point{8, math.Inf(-1)}},
		{"subnormal", Point{9, math.Float64frombits(1)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf := make([]byte, EncodedSize)
			if err := tc.p.Encode(buf); err != nil {
				t.Fatalf("encode: %v", err)
			}
			got, err := Decode(buf)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got.TS != tc.p.TS || math.Float64bits(got.Value) != math.Float64bits(tc.p.Value) {
				t.Fatalf("round trip mismatch: got %+v want %+v", got, tc.p)
			}
		})
	}
}

func TestPointErrorsAndBatch(t *testing.T) {
	cases := []struct {
		name string
		fn   func() error
		want error
	}{
		{"encode short", func() error { return Point{}.Encode(make([]byte, 15)) }, ErrShortBuffer},
		{"decode empty", func() error { _, e := Decode(nil); return e }, ErrShortBuffer},
		{"decode 15", func() error { _, e := Decode(make([]byte, 15)); return e }, ErrShortBuffer},
		{"valid nan", func() error { return Point{Value: math.NaN()}.Valid() }, ErrNaN},
		{"valid inf", func() error { return Point{Value: math.Inf(1)}.Valid() }, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.fn()
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
		})
	}

	points := []Point{{1, 1.5}, {2, 2.5}, {3, 3.5}}
	raw := EncodeAll(points)
	if len(raw) != 3*EncodedSize {
		t.Fatalf("encoded len = %d", len(raw))
	}
	got, err := DecodeAll(raw)
	if err != nil || len(got) != 3 {
		t.Fatalf("decode all: %v %d", err, len(got))
	}
	for i := range points {
		if got[i] != points[i] {
			t.Fatalf("batch[%d] = %+v", i, got[i])
		}
	}
	if _, err := DecodeAll(append(raw, 0xFF)); !errors.Is(err, ErrShortBuffer) {
		t.Fatalf("trailing byte should be ErrShortBuffer, got %v", err)
	}
	if !bytes.Equal(EncodeAll(nil), []byte{}) {
		t.Fatal("empty batch should encode to empty slice")
	}
}
