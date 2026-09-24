// Package vec defines fixed-dimension float64 vectors and their metrics.
package vec

import (
	"errors"
	"fmt"
	"math"
)

// ErrDimMismatch marks a vector whose dimension differs from the expected one.
var ErrDimMismatch = errors.New("vec: dimension mismatch")

// Vec is a float64 vector of fixed dimension.
type Vec []float64

// DimError reports a dimension mismatch with the expected and actual values.
func DimError(want, got int) error {
	return fmt.Errorf("%w: want %d, got %d", ErrDimMismatch, want, got)
}

// Check verifies that v has exactly dim components.
func Check(v Vec, dim int) error {
	if len(v) != dim {
		return DimError(dim, len(v))
	}
	return nil
}

// Valid reports whether v contains no NaN and no +/-Inf component.
func Valid(v Vec) bool {
	for _, x := range v {
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return false
		}
	}
	return true
}

// Dot returns the inner product of a and b, which must share dimension.
func Dot(a, b Vec) float64 {
	sum := 0.0
	for i := range a {
		sum += a[i] * b[i]
	}
	return sum
}

// Dist returns the Euclidean distance between a and b.
func Dist(a, b Vec) float64 {
	sum := 0.0
	for i := range a {
		d := a[i] - b[i]
		sum += d * d
	}
	return math.Sqrt(sum)
}
