// Package gauss inverts square matrices with Gauss-Jordan elimination and
// partial pivoting. It depends only on package pivot.
package gauss

import (
	"errors"
	"sync/atomic"

	"ontology/pivot"
)

// Sentinel errors: the three failure modes are distinct and errors.Is-able.
var (
	ErrEmptyMatrix  = errors.New("gauss: n must be >= 1")
	ErrBadDimension = errors.New("gauss: len(a) must equal n*n")
	ErrSingular     = errors.New("gauss: matrix is singular")
)

// rowOps counts multiply-subtract row operations (row_i -= m*row_j) across
// all calls. Pivot scans and normalizations do not count. It is unexported;
// callers can never read its value, only a built-in pass/fail verdict.
var rowOps atomic.Int64

// Invert returns A⁻¹ for the row-major n×n matrix a. The input slice is
// never modified; every row operation hits both halves of [A | I].
func Invert(a []float64, n int) (out []float64, err error) {
	if n < 1 {
		return nil, ErrEmptyMatrix
	}
	if len(a) != n*n {
		return nil, ErrBadDimension
	}
	l := make([]float64, n*n) // work on a copy: input is read-only
	copy(l, a)
	r := make([]float64, n*n)
	for i := 0; i < n; i++ { // exact identity: diagonal 1, off-diagonal 0
		r[i*n+i] = 1
	}
	var did int64
	commit := false
	defer func() {
		if !commit { // a rejected call must leave even the counter untouched
			rowOps.Add(-did)
		}
	}()
	if err = eliminate(l, r, n, &did); err != nil {
		return nil, err
	}
	commit = true
	return r, nil
}

// eliminate reduces l to I, mirroring every swap/scale/elimination onto r.
// Order per column: pivot, swap, normalize pivot row, eliminate below, then
// above. Only nonzero factors count as multiply-subtract row operations.
func eliminate(l, r []float64, n int, did *int64) error {
	for k := 0; k < n; k++ {
		p, ok := pivot.Pick(l, n, k)
		if !ok {
			return ErrSingular
		}
		if p != k {
			for j := 0; j < n; j++ { // swap both halves simultaneously
				l[k*n+j], l[p*n+j] = l[p*n+j], l[k*n+j]
				r[k*n+j], r[p*n+j] = r[p*n+j], r[k*n+j]
			}
		}
		inv := 1.0 / l[k*n+k] // Pick guarantees |pivot| > 0
		for j := 0; j < n; j++ {
			l[k*n+j] *= inv
			r[k*n+j] *= inv
		}
		for i := k + 1; i < n; i++ { // eliminate below first
			if m := l[i*n+k]; m != 0 {
				for j := 0; j < n; j++ {
					l[i*n+j] -= m * l[k*n+j]
					r[i*n+j] -= m * r[k*n+j]
				}
				rowOps.Add(1)
				*did++
			}
		}
		for i := 0; i < k; i++ { // then eliminate above
			if m := l[i*n+k]; m != 0 {
				for j := 0; j < n; j++ {
					l[i*n+j] -= m * l[k*n+j]
					r[i*n+j] -= m * r[k*n+j]
				}
				rowOps.Add(1)
				*did++
			}
		}
	}
	return nil
}

// RejectionsLeaveCounterUntouched verifies invariant 4 at the counter
// level: every rejected call (empty, bad dimension, singular) must leave
// rowOps exactly where it was. Verdict only; the value never escapes.
func RejectionsLeaveCounterUntouched() bool {
	before := rowOps.Load()
	cases := []struct {
		a []float64
		n int
	}{
		{nil, 0},
		{[]float64{1, 0, 0, 1}, 3}, // len 4 != 9
		{[]float64{0, 0, 0, 0}, 2}, // all-zero: singular
		{[]float64{1, 1, 1, 1}, 2}, // rank 1: column 1 vanishes
	}
	for _, c := range cases {
		if _, err := Invert(c.a, c.n); err == nil {
			return false
		}
	}
	return rowOps.Load() == before
}

// DiagonalNeedsNoRowOps is the complexity self-verification: inverting an
// identity-sized diagonal matrix of each given size must add zero
// multiply-subtract row operations. It exposes a verdict only, never the
// counter's value.
func DiagonalNeedsNoRowOps(sizes ...int) bool {
	for _, n := range sizes {
		d := make([]float64, n*n)
		for i := 0; i < n; i++ {
			d[i*n+i] = 1
		}
		before := rowOps.Load()
		if _, err := Invert(d, n); err != nil || rowOps.Load() != before {
			return false
		}
	}
	return true
}
