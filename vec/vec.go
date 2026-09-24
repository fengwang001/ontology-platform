// Package vec defines fixed-dimension float64 vectors and basic geometry.
package vec

import (
	"errors"
	"math"
)

// Vec is a fixed-dimension float64 vector.
type Vec []float64

// Classifiable, sentinel errors.
var (
	// ErrDimMismatch reports two vectors with different dimensions.
	ErrDimMismatch = errors.New("vec: dimension mismatch")
	// ErrInvalidVector reports an empty vector or NaN/±Inf components.
	ErrInvalidVector = errors.New("vec: invalid vector (empty or non-finite component)")
)

// Validate rejects empty vectors and NaN/±Inf components.
func Validate(x Vec) error {
	if len(x) == 0 {
		return ErrInvalidVector
	}
	for _, v := range x {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return ErrInvalidVector
		}
	}
	return nil
}

// CheckDim validates both vectors and asserts equal dimensions.
// The returned error wraps ErrDimMismatch / ErrInvalidVector.
func CheckDim(x, y Vec) error {
	if err := Validate(x); err != nil {
		return err
	}
	if err := Validate(y); err != nil {
		return err
	}
	if len(x) != len(y) {
		return dimError(len(x), len(y))
	}
	return nil
}

type dimErr struct{ want, got int }

func (e dimErr) Error() string {
	return "vec: dimension mismatch (want " + itoa(e.want) + ", got " + itoa(e.got) + ")"
}
func (e dimErr) Is(target error) bool { return target == ErrDimMismatch }

func dimError(want, got int) error { return dimErr{want: want, got: got} }

// DimMismatchError builds a classifiable dimension-mismatch error.
func DimMismatchError(want, got int) error { return dimError(want, got) }

// WantGot extracts expected and actual dimensions from a dimension-mismatch error.
func WantGot(err error) (want, got int, ok bool) {
	var d dimErr
	if errors.As(err, &d) {
		return d.want, d.got, true
	}
	return 0, 0, false
}

// Dot returns the inner product x·y.
func Dot(x, y Vec) (float64, error) {
	if err := CheckDim(x, y); err != nil {
		return 0, err
	}
	var sum float64
	for i := range x {
		sum += x[i] * y[i]
	}
	return sum, nil
}

// Euclidean returns the L2 distance between x and y.
func Euclidean(x, y Vec) (float64, error) {
	if err := CheckDim(x, y); err != nil {
		return 0, err
	}
	var sum float64
	for i := range x {
		d := x[i] - y[i]
		sum += d * d
	}
	return math.Sqrt(sum), nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
