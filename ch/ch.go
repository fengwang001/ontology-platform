// Package ch provides the fixed per-row hash functions of the sketch:
// h_j(x) = (a_j*x + b_j) mod w, with rows numbered from 1.
//
// Coefficients are fixed by the task:
//
//	j=1: a=1, b=0   j=2: a=2, b=1   j=3: a=5, b=3
//	j>3: a=2j-1, b=j
package ch

import "errors"

// ErrBadWidth signals a non-positive sketch width.
var ErrBadWidth = errors.New("ch: width must be positive")

// Family hashes keys into columns of one fixed width.
type Family struct {
	w int64
}

// NewFamily builds a Family of the given width.
func NewFamily(w int) (*Family, error) {
	if w <= 0 {
		return nil, ErrBadWidth
	}
	return &Family{w: int64(w)}, nil
}

// Coeff returns the (a_j, b_j) of 1-based row j.
func Coeff(j int) (a, b int64) {
	switch j {
	case 1:
		return 1, 0
	case 2:
		return 2, 1
	case 3:
		return 5, 3
	default:
		return int64(2*j - 1), int64(j)
	}
}

// Column returns h_j(x), the column hit by key x in row j (j >= 1).
// x must be non-negative; callers above this package enforce that.
func (f *Family) Column(j int, x int64) int {
	a, b := Coeff(j)
	return int((a*x + b) % f.w)
}
