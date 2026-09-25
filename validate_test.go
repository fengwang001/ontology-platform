package sparse

import (
	"errors"
	"math"
	"testing"
)

func TestNonStrictlyIncreasingIndicesLocated(t *testing.T) {
	cases := []struct {
		name                string
		a, b                *Vector
		wantVector, wantPos int
	}{
		{
			name: "equal indices in first vector",
			a: &Vector{Elems: []Element{
				{Index: 5, Value: 1}, {Index: 5, Value: 2},
			}},
			b:          &Vector{Elems: []Element{{Index: 5, Value: 1}}},
			wantVector: 0, wantPos: 1,
		},
		{
			name: "decreasing indices in second vector",
			a:    &Vector{Elems: []Element{{Index: 1, Value: 1}}},
			b: &Vector{Elems: []Element{
				{Index: 3, Value: 1}, {Index: 9, Value: 1}, {Index: 4, Value: 1},
			}},
			wantVector: 1, wantPos: 2,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := Dot(tc.a, tc.b)
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("err = %v, want *ValidationError", err)
			}
			if ve.Vector != tc.wantVector || ve.Position != tc.wantPos {
				t.Fatalf("got vector %d position %d, want vector %d position %d",
					ve.Vector, ve.Position, tc.wantVector, tc.wantPos)
			}
			// Cosine must surface the same validation error.
			if _, _, err := Cosine(tc.a, tc.b); !errors.As(err, &ve) {
				t.Fatalf("Cosine err = %v, want *ValidationError", err)
			}
		})
	}
}

func TestNaNValueLocated(t *testing.T) {
	a := &Vector{Elems: []Element{{Index: 0, Value: 1}}}
	b := &Vector{Elems: []Element{
		{Index: 0, Value: 1},
		{Index: 4, Value: math.NaN()},
	}}
	_, _, err := Dot(a, b)
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("err = %v, want *ValidationError", err)
	}
	if ve.Vector != 1 || ve.Position != 1 {
		t.Fatalf("got vector %d position %d, want vector 1 position 1",
			ve.Vector, ve.Position)
	}
}
