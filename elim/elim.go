// Package elim performs forward elimination with partial pivoting followed by
// back substitution. It depends only on the pivot package.
package elim

import (
	"errors"

	"ontology/pivot"
)

// Distinct, decidable sentinel errors. The three rejection causes never
// alias one another, so callers can branch with errors.Is.
var (
	// ErrDimension is returned when len(a) != n*n or len(b) != n.
	ErrDimension = errors.New("elim: dimension mismatch: len(a) must be n*n and len(b) must be n")
	// ErrEmpty is returned when n < 1.
	ErrEmpty = errors.New("elim: empty system: n must be >= 1")
	// ErrSingular is returned when a pivot column is zero at and below the
	// pivot row.
	ErrSingular = errors.New("elim: singular matrix: zero pivot column")
)

// Solve solves A x = b for the n*n row-major matrix a and length-n vector b
// using Gaussian elimination with partial pivoting. The inputs are never
// modified; all arithmetic is on internal copies, so concurrent callers may
// share the same a and b.
//
// All validation runs before any allocation or copy, and singularity is
// discovered only on the working copies, so every rejected call fails
// wholesale without touching the inputs or any counter.
func Solve(a, b []float64, n int) ([]float64, error) {
	if n < 1 {
		return nil, ErrEmpty
	}
	if len(a) != n*n || len(b) != n {
		return nil, ErrDimension
	}

	// Defensive full copies: the caller's a and b are never written.
	u := append([]float64(nil), a...)
	y := append([]float64(nil), b...)

	// Forward elimination with partial pivoting. Row slices let the compiler
	// hoist bounds checks out of the hot inner loop.
	for k := 0; k < n; k++ {
		p, ok := pivot.Pick(u, n, k)
		if !ok {
			return nil, ErrSingular
		}
		if p != k {
			// Row exchange must move both the matrix row and the rhs entry
			// together; they stay one-to-one.
			rk, rp := u[k*n:(k+1)*n], u[p*n:(p+1)*n]
			for j := range rk {
				rk[j], rp[j] = rp[j], rk[j]
			}
			y[k], y[p] = y[p], y[k]
		}
		prow := u[k*n : (k+1)*n]
		piv := prow[k] // pivot of the swapped row k
		for i := k + 1; i < n; i++ {
			ri := u[i*n : (i+1)*n]
			m := ri[k] / piv
			ri[k] = 0 // row_i -= m*row_k makes column k exactly zero
			for j := k + 1; j < n; j++ {
				ri[j] -= m * prow[j]
			}
			y[i] -= m * y[k]
		}
	}

	// Back substitution from x[n-1] down to x[0].
	x := make([]float64, n)
	for i := n - 1; i >= 0; i-- {
		s := y[i]
		for j := i + 1; j < n; j++ {
			s -= u[i*n+j] * x[j]
		}
		x[i] = s / u[i*n+i]
	}
	return x, nil
}
