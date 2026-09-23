package agg

import (
	"errors"
	"math"
	"testing"

	"ontology/point"
)

func pts(vv ...float64) []point.Point {
	out := make([]point.Point, len(vv))
	for i, v := range vv {
		out[i] = point.Point{TS: int64(i), Value: v}
	}
	return out
}

func TestAggregate(t *testing.T) {
	cases := []struct {
		name string
		in   []point.Point
		want Result
	}{
		{"single", pts(4), Result{4, 4, 4, 4, 4, 1}},
		{"ordered", pts(1, 2, 3, 4), Result{1, 4, 1, 4, 2.5, 4}},
		{"unsortedValues", pts(3, -1, 2, 10), Result{3, 10, -1, 10, 3.5, 4}},
		{"negatives", pts(-5, -2, -8), Result{-5, -8, -8, -2, -5, 3}},
		{"plusInf", pts(1, math.Inf(1)), Result{1, math.Inf(1), 1, math.Inf(1), math.Inf(1), 2}},
		{"minusInf", pts(math.Inf(-1), 1), Result{math.Inf(-1), 1, math.Inf(-1), 1, math.Inf(-1), 2}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Aggregate(tc.in)
			if err != nil {
				t.Fatalf("aggregate: %v", err)
			}
			if got.Count != tc.want.Count ||
				math.Float64bits(got.First) != math.Float64bits(tc.want.First) ||
				math.Float64bits(got.Last) != math.Float64bits(tc.want.Last) ||
				math.Float64bits(got.Min) != math.Float64bits(tc.want.Min) ||
				math.Float64bits(got.Max) != math.Float64bits(tc.want.Max) ||
				math.Float64bits(got.Mean) != math.Float64bits(tc.want.Mean) {
				t.Fatalf("got %+v want %+v", got, tc.want)
			}
		})
	}
}

func TestEmptyAndDeterminism(t *testing.T) {
	if _, err := Aggregate(nil); !errors.Is(err, ErrEmpty) {
		t.Fatalf("empty: want ErrEmpty, got %v", err)
	}
	a, _ := Aggregate(pts(1, 2, 3, 4))
	b, _ := Aggregate(pts(1, 2, 3, 4))
	if math.Float64bits(a.Mean) != math.Float64bits(b.Mean) {
		t.Fatal("mean not bit-deterministic")
	}
}
