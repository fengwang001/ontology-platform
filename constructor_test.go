package ontology

import (
	"errors"
	"math"
	"testing"
)

func TestConstructionErrors(t *testing.T) {
	tests := []struct {
		name string
		lo   float64
		hi   float64
		n    int
		want error
	}{
		{"non-positive n", 0, 1, 0, ErrInvalidBucketCount},
		{"empty range", 1, 1, 3, ErrInvalidRange},
		{"reversed range", 2, 1, 3, ErrInvalidRange},
		{"NaN lower", math.NaN(), 1, 3, ErrNaNBound},
		{"NaN upper", 0, math.NaN(), 3, ErrNaNBound},
		{"negative infinite lower", math.Inf(-1), 1, 3, ErrInfBound},
		{"positive infinite upper", 0, math.Inf(1), 3, ErrInfBound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(tt.lo, tt.hi, tt.n)
			if !errors.Is(err, tt.want) {
				t.Fatalf("New() error = %v, want errors.Is %v", err, tt.want)
			}
		})
	}
}

func TestConstructionErrorsAreDistinct(t *testing.T) {
	sentinels := []error{
		ErrInvalidBucketCount,
		ErrInvalidRange,
		ErrNaNBound,
		ErrInfBound,
	}

	for i, first := range sentinels {
		for _, second := range sentinels[i+1:] {
			if errors.Is(first, second) {
				t.Fatalf("sentinel errors %v and %v are not distinct", first, second)
			}
		}
	}
}
