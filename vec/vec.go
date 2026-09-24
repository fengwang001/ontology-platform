// Package vec provides fixed-dimension float64 vector primitives.
package vec

import (
	"errors"
	"fmt"
	"math"
)

// Vec is a fixed-dimension float64 vector.
type Vec []float64

var (
	// ErrDimMismatch indicates two vectors (or query vs index) differ in dim.
	ErrDimMismatch = errors.New("vec: dimension mismatch")
	// ErrBadValue indicates a component is NaN or Inf.
	ErrBadValue = errors.New("vec: NaN or Inf component")
)

// DimError reports expected and actual dimensions and satisfies errors.Is(ErrDimMismatch).
type DimError struct {
	Want, Got int
}

func (e DimError) Error() string {
	return fmt.Sprintf("vec: dimension mismatch: want %d got %d", e.Want, e.Got)
}
func (e DimError) Is(target error) bool { return target == ErrDimMismatch }

// CheckDim returns a typed DimError when lens differ.
func CheckDim(a, b Vec) error {
	if len(a) != len(b) {
		return DimError{Want: len(a), Got: len(b)}
	}
	return nil
}

// Validate rejects NaN and Inf components.
func Validate(x Vec) error {
	for _, v := range x {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return ErrBadValue
		}
	}
	return nil
}

// Dot returns the inner product <a,b>; lens must match.
func Dot(a, b Vec) (float64, error) {
	if err := CheckDim(a, b); err != nil {
		return 0, err
	}
	var s float64
	for i := range a {
		s += a[i] * b[i]
	}
	return s, nil
}

// SquaredL2 returns the squared Euclidean distance (monotone with L2 for ranking).
func SquaredL2(a, b Vec) (float64, error) {
	if err := CheckDim(a, b); err != nil {
		return 0, err
	}
	var s float64
	for i := range a {
		d := a[i] - b[i]
		s += d * d
	}
	return s, nil
}

// L2 returns the Euclidean distance.
func L2(a, b Vec) (float64, error) {
	s, err := SquaredL2(a, b)
	if err != nil {
		return 0, err
	}
	return math.Sqrt(s), nil
}
