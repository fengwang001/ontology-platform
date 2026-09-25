// Package vec defines dense float64 vectors and their basic operations.
package vec

import (
	"errors"
	"fmt"
	"math"
)

// Sentinel errors, distinguishable with errors.Is.
var (
	ErrDimMismatch = errors.New("vec: dimension mismatch")
	ErrNaN         = errors.New("vec: vector contains NaN")
	ErrInf         = errors.New("vec: vector contains Inf")
)

// Vector is a dense fixed-dimension vector.
type Vector []float64

// DimError reports an expected vs actual dimension mismatch.
type DimError struct {
	Want int
	Got  int
}

func (e DimError) Error() string {
	return fmt.Sprintf("vec: dimension mismatch: want %d, got %d", e.Want, e.Got)
}

// Unwrap lets errors.Is(err, ErrDimMismatch) match.
func (e DimError) Unwrap() error { return ErrDimMismatch }

// Check validates v against dim and rejects NaN and ±Inf components.
func Check(v Vector, dim int) error {
	if len(v) != dim {
		return DimError{Want: dim, Got: len(v)}
	}
	for _, x := range v {
		if math.IsNaN(x) {
			return ErrNaN
		}
		if math.IsInf(x, 0) {
			return ErrInf
		}
	}
	return nil
}

// Dot returns the inner product of a and b, which must have equal length.
func Dot(a, b Vector) float64 {
	sum := 0.0
	for i := range a {
		sum += a[i] * b[i]
	}
	return sum
}

// Dist returns the Euclidean distance between a and b (equal length).
func Dist(a, b Vector) float64 {
	sum := 0.0
	for i := range a {
		d := a[i] - b[i]
		sum += d * d
	}
	return math.Sqrt(sum)
}
