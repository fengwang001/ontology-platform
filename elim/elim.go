// Package elim computes determinants by Gaussian elimination with partial pivoting.
package elim

import (
	"errors"
	"math"
	"sync/atomic"

	"ontology/pivot"
)

// Sentinel errors for distinguishable rejection.
var (
	ErrEmpty    = errors.New("elim: empty matrix")
	ErrDim      = errors.New("elim: dimension mismatch")
	ErrNaNOrInf = errors.New("elim: matrix contains NaN or Inf")
)

// ops counts multiply-subtract row operations performed across all Det calls.
// Pivot scans are not counted. It is unexported package state on purpose; only
// white-box tests in this package may read it.
var ops atomic.Int64

// Det returns the determinant of the n×n row-major matrix a by partial-pivot
// Gaussian elimination. The input is never modified: elimination runs on a
// copy. A singular matrix is not an error: Det returns (0, nil). All rejection
// checks happen before any state change, so a rejected call leaves no trace.
func Det(a []float64, n int) (float64, error) {
	if n < 1 {
		return 0, ErrEmpty
	}
	if len(a) != n*n {
		return 0, ErrDim
	}
	for _, v := range a {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return 0, ErrNaNOrInf
		}
	}

	m := make([]float64, n*n)
	copy(m, a)

	sign := 1.0
	for k := 0; k < n; k++ {
		r, ok := pivot.Pick(m, n, k)
		if !ok {
			return 0, nil // singular column at and below the diagonal: det = 0
		}
		if r != k {
			kr, rr := k*n, r*n
			for j := 0; j < n; j++ {
				m[kr+j], m[rr+j] = m[rr+j], m[kr+j]
			}
			sign = -sign
		}
		piv := m[k*n+k]
		for i := k + 1; i < n; i++ {
			if m[i*n+k] == 0 {
				continue // already eliminated column: no row operation
			}
			f := m[i*n+k] / piv
			for j := k; j < n; j++ {
				m[i*n+j] -= f * m[k*n+j]
			}
			ops.Add(1)
		}
	}

	det := sign
	for k := 0; k < n; k++ {
		det *= m[k*n+k]
	}
	return det, nil
}

// VerifyTriangularNoOps runs Det on already-upper-triangular matrices of the
// given sizes and returns an error if elimination performs any
// multiply-subtract row operation. Only pass/fail crosses the boundary; the
// internal counter value is never exposed.
func VerifyTriangularNoOps(sizes ...int) error {
	for _, n := range sizes {
		before := ops.Load()
		m := make([]float64, n*n)
		for i := 0; i < n; i++ {
			m[i*n+i] = 1
			for j := i + 1; j < n; j++ {
				m[i*n+j] = float64((i + j) % 5)
			}
		}
		d, err := Det(m, n)
		if err != nil || d != 1 || ops.Load() != before {
			return errors.New("elim: upper-triangular matrix triggered row operations")
		}
	}
	return nil
}

// VerifyRejectionNoTrace fires every rejection and checks that the internal
// operation counter does not move and valid matrices still serve afterwards.
// Only pass/fail is returned.
func VerifyRejectionNoTrace() error {
	a := []float64{0, 1, 1, 1, 0, 1, 1, 1, 0}
	if d, err := Det(a, 3); err != nil || d != 2 {
		return errors.New("elim: baseline det before rejection failed")
	}
	before := ops.Load()
	cases := []struct {
		a []float64
		n int
		e error
	}{
		{nil, 0, ErrEmpty},
		{[]float64{1}, -1, ErrEmpty},
		{[]float64{1, 2, 3}, 2, ErrDim},
		{[]float64{1, math.NaN(), 0, 1}, 2, ErrNaNOrInf},
		{[]float64{1, math.Inf(1), 0, 1}, 2, ErrNaNOrInf},
	}
	for _, c := range cases {
		if _, err := Det(c.a, c.n); !errors.Is(err, c.e) {
			return errors.New("elim: unexpected rejection error")
		}
	}
	if ops.Load() != before {
		return errors.New("elim: rejection changed internal state")
	}
	if d, err := Det(a, 3); err != nil || d != 2 {
		return errors.New("elim: unusable after rejection")
	}
	return nil
}
