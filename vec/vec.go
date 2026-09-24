// Package vec defines fixed-dimensional float64 vectors and basic geometry.
package vec

import (
	"errors"
	"math"
)

// Vector is a fixed-length float64 vector.
type Vector []float64

// ErrDimMismatch indicates two vectors have incompatible dimensions.
var ErrDimMismatch = errors.New("vec: dimension mismatch")

// DimError reports expected and actual dimensions. It wraps ErrDimMismatch.
type DimError struct {
	Expected int
	Actual   int
}

func (e DimError) Error() string {
	return "vec: dimension mismatch: expected " + itoa(e.Expected) +
		", got " + itoa(e.Actual)
}

func (e DimError) Is(target error) bool { return target == ErrDimMismatch }

// CheckDim returns a DimError when dims differ.
func CheckDim(want, got int) error {
	if want != got {
		return DimError{Expected: want, Actual: got}
	}
	return nil
}

// Dim returns the vector dimension.
func (v Vector) Dim() int { return len(v) }

// Dot returns the inner product. Caller guarantees equal dimensions.
func Dot(a, b Vector) (float64, error) {
	if err := CheckDim(len(a), len(b)); err != nil {
		return 0, err
	}
	var sum float64
	for i := range a {
		sum += a[i] * b[i]
	}
	return sum, nil
}

// Euclidean returns the L2 distance between a and b.
func Euclidean(a, b Vector) (float64, error) {
	if err := CheckDim(len(a), len(b)); err != nil {
		return 0, err
	}
	var sum float64
	for i := range a {
		d := a[i] - b[i]
		sum += d * d
	}
	return math.Sqrt(sum), nil
}

// SquaredEuclidean returns the squared L2 distance (cheaper for ranking).
func SquaredEuclidean(a, b Vector) (float64, error) {
	if err := CheckDim(len(a), len(b)); err != nil {
		return 0, err
	}
	var sum float64
	for i := range a {
		d := a[i] - b[i]
		sum += d * d
	}
	return sum, nil
}

// Validate rejects NaN and Inf components. Empty/zero vectors are legal.
func Validate(v Vector) error {
	for _, x := range v {
		if math.IsNaN(x) {
			return errors.New("vec: component is NaN")
		}
		if math.IsInf(x, 0) {
			return errors.New("vec: component is Inf")
		}
	}
	return nil
}

// HasNaN reports whether any component is NaN.
func HasNaN(v Vector) bool {
	for _, x := range v {
		if math.IsNaN(x) {
			return true
		}
	}
	return false
}

// HasInf reports whether any component is +Inf or -Inf.
func HasInf(v Vector) bool {
	for _, x := range v {
		if math.IsInf(x, 0) {
			return true
		}
	}
	return false
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
