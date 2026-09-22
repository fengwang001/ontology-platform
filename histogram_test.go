package ontology

import (
	"errors"
	"math"
	"testing"
)

func TestNewHistogramErrors(t *testing.T) {
	cases := []struct {
		name string
		lo   float64
		hi   float64
		n    int
		want error
	}{
		{"n zero", 0, 1, 0, ErrInvalidBuckets},
		{"n negative", 0, 1, -3, ErrInvalidBuckets},
		{"lo equals hi", 1, 1, 4, ErrInvalidRange},
		{"lo above hi", 2, 1, 4, ErrInvalidRange},
		{"lo NaN", math.NaN(), 1, 4, ErrInvalidBound},
		{"hi NaN", 0, math.NaN(), 4, ErrInvalidBound},
		{"lo +Inf", math.Inf(1), 1, 4, ErrInvalidBound},
		{"hi -Inf", 0, math.Inf(-1), 4, ErrInvalidBound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewHistogram(tc.lo, tc.hi, tc.n)
			if !errors.Is(err, tc.want) {
				t.Fatalf("NewHistogram(%v,%v,%d): want %v, got %v",
					tc.lo, tc.hi, tc.n, tc.want, err)
			}
		})
	}
}

func TestNewHistogramOK(t *testing.T) {
	h, err := NewHistogram(-1, 2, 7)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h.Lo() != -1 || h.Hi() != 2 || h.N() != 7 {
		t.Fatalf("unexpected spec: (%v,%v,%d)", h.Lo(), h.Hi(), h.N())
	}
	if got := h.Buckets(); len(got) != 7 {
		t.Fatalf("want 7 buckets, got %d", len(got))
	}
}
