package stats

import (
	"errors"
	"math"
	"testing"
)

func TestSelectivity(t *testing.T) {
	cases := []struct {
		name    string
		c1, c2  *Column
		wantSel float64
		missing bool
	}{
		{"both ndv", &Column{NDV: 10}, &Column{NDV: 50}, 0.02, false},
		{"equal ndv", &Column{NDV: 7}, &Column{NDV: 7}, 1.0 / 7, false},
		{"nil left", nil, &Column{NDV: 10}, DefaultSelectivity, true},
		{"zero ndv", &Column{NDV: 0}, &Column{NDV: 10}, DefaultSelectivity, true},
		{"both nil", nil, nil, DefaultSelectivity, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sel, missing := Selectivity(tc.c1, tc.c2)
			if math.Abs(sel-tc.wantSel) > 1e-15 || missing != tc.missing {
				t.Fatalf("got (%v,%v), want (%v,%v)", sel, missing, tc.wantSel, tc.missing)
			}
		})
	}
}

func TestHistogramValidate(t *testing.T) {
	cases := []struct {
		name string
		h    Histogram
		rows int64
		ok   bool
	}{
		{"valid", Histogram{Bounds: []float64{0, 1, 2}, Counts: []int64{3, 7}}, 10, true},
		{"sum mismatch", Histogram{Bounds: []float64{0, 1, 2}, Counts: []int64{3, 6}}, 10, false},
		{"non increasing", Histogram{Bounds: []float64{0, 2, 2}, Counts: []int64{3, 7}}, 10, false},
		{"shape mismatch", Histogram{Bounds: []float64{0, 1}, Counts: []int64{3, 7}}, 10, false},
		{"zero rows valid", Histogram{Bounds: []float64{0, 1}, Counts: []int64{0}}, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.h.Validate(tc.rows)
			if (err == nil) != tc.ok || (err != nil && !errors.Is(err, ErrCorruptHistogram)) {
				t.Fatalf("got err=%v ok=%t, want ok=%t", err, err == nil, tc.ok)
			}
		})
	}
}
