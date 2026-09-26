// Package api is the public entry point for Gauss-Jordan matrix inversion.
// It depends only on package gauss.
package api

import (
	"errors"
	"math"
	"slices"
	"sync/atomic"

	"ontology/gauss"
)

// Sentinel errors: the three failure modes stay distinct and errors.Is-able.
var (
	ErrEmptyMatrix  = gauss.ErrEmptyMatrix
	ErrBadDimension = gauss.ErrBadDimension
	ErrSingular     = gauss.ErrSingular
)

const eps = 1e-9

// API inverts matrices. The unexported accepted counter is advanced only by
// successful calls, so every rejected operation leaves zero state behind;
// its value is never exposed.
type API struct {
	accepted atomic.Int64
}

// New returns a ready-to-use inversion service.
func New() *API { return &API{} }

// Inv returns A⁻¹ for the row-major n×n matrix a. a is read-only and safe
// for concurrent use; dimension/empty/singular failures change no state.
func (x *API) Inv(a []float64, n int) ([]float64, error) {
	out, err := gauss.Invert(a, n)
	if err != nil {
		return nil, err
	}
	x.accepted.Add(1)
	return out, nil
}

// deviation returns max |(A·B)[i][j] - I[i][j]|.
func deviation(a, b []float64, n int) (d float64) {
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			s := 0.0
			for k := 0; k < n; k++ {
				s += a[i*n+k] * b[k*n+j]
			}
			if i == j {
				s--
			}
			if e := math.Abs(s); e > d {
				d = e
			}
		}
	}
	return d
}

// SelfCheck verifies the four invariants on a built-in set and returns one
// verdict: A·A⁻¹==I within 1e-9; Inv(I) is exactly I; the input slice is
// byte-identical afterwards; three distinct rejections change no state and
// the service stays usable.
func (x *API) SelfCheck() bool {
	cases := []struct {
		a []float64
		n int
	}{
		{[]float64{2}, 1},
		{[]float64{0, 2, 1, 1}, 2}, // the NOTES.md derivation matrix
		{[]float64{4, -1, 0, -1, 4, -1, 0, -1, 4}, 3},
		{[]float64{5, -1, 0, 0, -1, 5, -1, 0, 0, -1, 5, -1, 0, 0, -1, 5}, 4},
	}
	for _, c := range cases {
		snap := slices.Clone(c.a)
		inv, err := x.Inv(c.a, c.n)
		if err != nil || !slices.Equal(c.a, snap) { // invariants 1+3
			return false
		}
		if deviation(c.a, inv, c.n) > eps { // A·A⁻¹ == I
			return false
		}
	}
	id := []float64{1, 0, 0, 0, 1, 0, 0, 0, 1} // invariant 2: Inv(I) is exactly I
	if inv, err := x.Inv(id, 3); err != nil || !slices.Equal(inv, id) {
		return false
	}
	bad := []struct {
		a    []float64
		n    int
		want error
	}{
		{nil, 0, ErrEmptyMatrix},
		{[]float64{1, 0, 0, 1}, 3, ErrBadDimension},
		{[]float64{1, 1, 1, 1}, 2, ErrSingular},
	}
	before := x.accepted.Load() // invariant 4: rejections leave no trace
	for _, b := range bad {
		if _, err := x.Inv(b.a, b.n); !errors.Is(err, b.want) {
			return false
		}
	}
	if x.accepted.Load() != before {
		return false
	}
	if _, err := x.Inv([]float64{1, 0, 0, 1}, 2); err != nil { // still usable
		return false
	}
	return errors.Is(ErrSingular, ErrSingular) && ErrEmptyMatrix != ErrBadDimension &&
		ErrBadDimension != ErrSingular && ErrEmptyMatrix != ErrSingular
}
