package ontology

import (
	"math"
	"testing"
)

func TestCanonicalBoundaryOwnership(t *testing.T) {
	h, err := New(0, 1, 3)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		x          float64
		wantBucket int
	}{
		{"lower bound", 0, 0},
		{"internal boundary", 1.0 / 3.0, 1},
		{"last representable sample", math.Nextafter(1, math.Inf(-1)), 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := BucketIndex(0, 1, 3, tt.x)
			if err != nil || got != tt.wantBucket {
				t.Fatalf("BucketIndex(%v) = (%d, %v), want %d", tt.x, got, err, tt.wantBucket)
			}
			if err := h.Add(tt.x); err != nil {
				t.Fatal(err)
			}
		})
	}

	if got := h.Overflow(); got != 0 {
		t.Fatalf("overflow before adding hi = %d, want 0", got)
	}
	if err := h.Add(1); err != nil {
		t.Fatal(err)
	}
	if got := h.Overflow(); got != 1 {
		t.Fatalf("overflow after adding hi = %d, want 1", got)
	}

	buckets := h.Buckets()
	want := []uint64{1, 1, 1}
	for i := range want {
		if buckets[i] != want[i] {
			t.Fatalf("buckets = %v, want %v", buckets, want)
		}
	}
}

func TestCorrectedIndexForNonDivisibleWidths(t *testing.T) {
	tests := []struct {
		lo, hi     float64
		n          int
		boundary   int
		naiveWrong bool
	}{
		{0, 1, 3, 1, false},
		{-1, 2, 7, 3, false},
		{-10, -9, 3, 2, true},
	}

	for _, tt := range tests {
		width := (tt.hi - tt.lo) / float64(tt.n)
		x := tt.lo + float64(tt.boundary)*width
		naive := NaiveBucketIndex(tt.lo, tt.hi, tt.n, x)
		corrected, err := BucketIndex(tt.lo, tt.hi, tt.n, x)
		if err != nil {
			t.Fatal(err)
		}

		if corrected != tt.boundary {
			t.Fatalf("corrected index for (%v,%v,%d), x=%.20g = %d, want %d",
				tt.lo, tt.hi, tt.n, x, corrected, tt.boundary)
		}
		if tt.naiveWrong && naive == corrected {
			t.Fatalf("sample %.20g did not expose naive formula; naive=%d", x, naive)
		}
	}
}
