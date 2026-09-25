package sparse

import (
	"errors"
	"math"
	"testing"
)

func TestCosineIdenticalVectorsExactlyOne(t *testing.T) {
	v := &Vector{Elems: []Element{
		{Index: 0, Value: 1e16},
		{Index: 3, Value: 1},
		{Index: 7, Value: -1e16},
		{Index: 12, Value: 0.1},
		{Index: 20, Value: 7.25},
	}}
	cos, _, err := Cosine(v, v)
	if err != nil {
		t.Fatalf("Cosine: %v", err)
	}
	if math.Float64bits(cos) != math.Float64bits(1) {
		t.Fatalf("cos = %v (bits %x), want exactly 1", cos, math.Float64bits(cos))
	}
}

func TestCosineZeroNormVectors(t *testing.T) {
	normal := &Vector{Elems: []Element{{Index: 0, Value: 1}}}
	empty := &Vector{}
	allZero := &Vector{Elems: []Element{
		{Index: 0, Value: 0}, {Index: 5, Value: 0},
	}}
	cases := []struct {
		name       string
		a, b       *Vector
		wantVector int
	}{
		{"empty first", empty, normal, 0},
		{"empty second", normal, empty, 1},
		{"all-zero first", allZero, normal, 0},
		{"all-zero second", normal, allZero, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cos, _, err := Cosine(tc.a, tc.b)
			var zn *ZeroNormError
			if !errors.As(err, &zn) {
				t.Fatalf("err = %v, want *ZeroNormError", err)
			}
			if zn.Vector != tc.wantVector {
				t.Fatalf("ZeroNormError.Vector = %d, want %d", zn.Vector, tc.wantVector)
			}
			if cos != 0 {
				t.Fatalf("cos = %v on error, want 0", cos)
			}
		})
	}
}

func TestCosineResultClampedToUnitInterval(t *testing.T) {
	// Near-parallel vectors: rounding could push the raw quotient
	// slightly past 1; the result must stay within [-1, 1].
	a := &Vector{Elems: []Element{
		{Index: 0, Value: 1},
		{Index: 1, Value: 1e-9},
	}}
	b := &Vector{Elems: []Element{
		{Index: 0, Value: 1},
		{Index: 1, Value: 1.0000000001e-9},
	}}
	cos, _, err := Cosine(a, b)
	if err != nil {
		t.Fatalf("Cosine: %v", err)
	}
	if cos < -1 || cos > 1 {
		t.Fatalf("cos = %v, outside [-1, 1]", cos)
	}
	// Anti-parallel vectors give -1.
	neg := &Vector{Elems: []Element{
		{Index: 0, Value: -1},
		{Index: 1, Value: -1e-9},
	}}
	cos, _, err = Cosine(a, neg)
	if err != nil {
		t.Fatalf("Cosine: %v", err)
	}
	if cos < -1 || cos > 1 || cos > -0.99 {
		t.Fatalf("cos = %v, want within [-1, 1] near -1", cos)
	}
	// Orthogonal vectors give 0.
	x := &Vector{Elems: []Element{{Index: 0, Value: 3}}}
	y := &Vector{Elems: []Element{{Index: 1, Value: 4}}}
	cos, _, err = Cosine(x, y)
	if err != nil {
		t.Fatalf("Cosine: %v", err)
	}
	if cos != 0 {
		t.Fatalf("cos = %v, want 0 for orthogonal vectors", cos)
	}
}

func TestCosineInfiniteValuesReturnError(t *testing.T) {
	inf := &Vector{Elems: []Element{{Index: 0, Value: math.Inf(1)}}}
	normal := &Vector{Elems: []Element{{Index: 0, Value: 2}}}
	for _, tc := range [][2]*Vector{{inf, normal}, {normal, inf}, {inf, inf}} {
		_, _, err := Cosine(tc[0], tc[1])
		var ne *NumericError
		if !errors.As(err, &ne) {
			t.Fatalf("err = %v, want *NumericError", err)
		}
	}
}

func TestCosineRepeatedCallsBitIdentical(t *testing.T) {
	a := &Vector{Elems: []Element{
		{Index: 1, Value: 1e16}, {Index: 4, Value: 1}, {Index: 9, Value: -1e16},
	}}
	b := &Vector{Elems: []Element{
		{Index: 1, Value: 2}, {Index: 4, Value: 3}, {Index: 9, Value: 1},
	}}
	first, _, err := Cosine(a, b)
	if err != nil {
		t.Fatalf("Cosine: %v", err)
	}
	for i := 0; i < 100; i++ {
		got, _, err := Cosine(a, b)
		if err != nil {
			t.Fatalf("Cosine: %v", err)
		}
		if math.Float64bits(got) != math.Float64bits(first) {
			t.Fatalf("iteration %d: bits differ", i)
		}
	}
}
