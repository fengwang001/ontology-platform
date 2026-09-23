package point

import (
	"errors"
	"math"
	"testing"
)

func TestEncodeDecode(t *testing.T) {
	cases := []struct {
		name string
		p    Point
	}{
		{"zero", Point{}},
		{"positive", Point{TS: 42, Value: 3.5}},
		{"negative", Point{TS: -1, Value: -7.25}},
		{"inf", Point{TS: 1, Value: math.Inf(1)}},
		{"ninf", Point{TS: 2, Value: math.Inf(-1)}},
		{"nan", Point{TS: 3, Value: math.NaN()}},
		{"max", Point{TS: math.MaxInt64, Value: math.MaxFloat64}},
		{"min", Point{TS: math.MinInt64, Value: math.SmallestNonzeroFloat64}},
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
				t.Fatalf("round trip %v -> %v", tc.p, got)
			}
		})
	}
}

func TestShortBuffer(t *testing.T) {
	cases := [][]byte{nil, {}, make([]byte, EncodedSize-1)}
	for i, src := range cases {
		if _, err := Decode(src); !errors.Is(err, ErrShortBuffer) {
			t.Fatalf("case %d decode: want ErrShortBuffer, got %v", i, err)
		}
		if err := (Point{}).Encode(src); !errors.Is(err, ErrShortBuffer) {
			t.Fatalf("case %d encode: want ErrShortBuffer, got %v", i, err)
		}
	}
}
