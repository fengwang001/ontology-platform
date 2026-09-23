package agg

import (
	"math"
	"testing"
)

func TestAccumulator(t *testing.T) {
	cases := []struct {
		name  string
		vals  []float64
		min   float64
		max   float64
		mean  float64
		count int64
	}{
		{"basic", []float64{1, 2, 3, 4}, 1, 4, 2.5, 4},
		{"unordered", []float64{4, -1, 2, 10}, -1, 10, 3.75, 4},
		{"single", []float64{7}, 7, 7, 7, 1},
		{"negatives", []float64{-5, -1, -9}, -9, -1, -5, 3},
		{"plus inf", []float64{1, math.Inf(1), 2}, 1, math.Inf(1), math.Inf(1), 3},
		{"minus inf", []float64{1, math.Inf(-1)}, math.Inf(-1), 1, math.Inf(-1), 2},
		{"inf plus minus inf nan", []float64{math.Inf(1), math.Inf(-1)},
			math.Inf(-1), math.Inf(1), math.NaN(), 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := New(100)
			for _, v := range tc.vals {
				a.Add(v)
			}
			b := a.Result()
			if a.Count() != tc.count || b.Count != tc.count {
				t.Fatalf("count = %d/%d want %d", a.Count(), b.Count, tc.count)
			}
			wantFirst, wantLast := bitExtremes(tc.vals)
			if math.Float64bits(b.First) != math.Float64bits(wantFirst) ||
				math.Float64bits(b.Last) != math.Float64bits(wantLast) {
				t.Fatalf("first/last = %v/%v want %v/%v", b.First, b.Last, wantFirst, wantLast)
			}
			if b.Start != 100 || b.Min != tc.min || b.Max != tc.max {
				t.Fatalf("bucket = %+v", b)
			}
			if math.IsNaN(tc.mean) {
				if !math.IsNaN(b.Mean) {
					t.Fatalf("mean = %v want NaN", b.Mean)
				}
			} else if math.Float64bits(b.Mean) != math.Float64bits(tc.mean) {
				t.Fatalf("mean bits = %x want %x", math.Float64bits(b.Mean),
					math.Float64bits(tc.mean))
			}
		})
	}
}

func TestEmptyAccumulator(t *testing.T) {
	b := New(0).Result()
	if b.Count != 0 || b.Mean != 0 || b.Start != 0 {
		t.Fatalf("empty bucket should be zero-valued, got %+v", b)
	}
}
