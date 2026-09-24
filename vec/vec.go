// Package vec defines fixed-dimension float64 vectors and their metrics.
package vec

import (
	"errors"
	"fmt"
	"math"
)

// Vec is a float64 vector of fixed dimension.
type Vec []float64

// ErrDim marks dimension-mismatch errors; match with errors.Is.
var ErrDim = errors.New("vec: dimension mismatch")

// DimError reports an expected vs actual dimension mismatch.
type DimError struct {
	Want, Got int
}

func (e DimError) Error() string {
	return fmt.Sprintf("%s: want %d, got %d", ErrDim, e.Want, e.Got)
}

// Is lets errors.Is(err, ErrDim) match any DimError.
func (e DimError) Is(target error) bool { return target == ErrDim }

func check(a, b Vec) error {
	if len(a) != len(b) {
		return DimError{Want: len(a), Got: len(b)}
	}
	return nil
}

// Dot returns the inner product of a and b.
func Dot(a, b Vec) (float64, error) {
	if err := check(a, b); err != nil {
		return 0, err
	}
	sum := 0.0
	for i := range a {
		sum += a[i] * b[i]
	}
	return sum, nil
}

// Dist returns the Euclidean distance between a and b.
func Dist(a, b Vec) (float64, error) {
	if err := check(a, b); err != nil {
		return 0, err
	}
	sum := 0.0
	for i := range a {
		d := a[i] - b[i]
		sum += d * d
	}
	return math.Sqrt(sum), nil
}

// Finite reports whether every component of v is finite (no NaN, no ±Inf).
func Finite(v Vec) bool {
	for _, x := range v {
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return false
		}
	}
	return true
}
