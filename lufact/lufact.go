// Package lufact performs a single Doolittle LU factorization without pivoting.
// It depends on no other package in this module.
package lufact

import "errors"

// Sentinel errors are mutually distinct and usable with errors.Is.
var (
	// ErrDimension: len(a) is not n*n (also reused for len(b) != n upstream).
	ErrDimension = errors.New("lufact: dimension mismatch")
	// ErrEmpty: n is less than 1.
	ErrEmpty = errors.New("lufact: empty system (n < 1)")
	// ErrZeroPivot: a required pivot A[k][k] is exactly zero.
	ErrZeroPivot = errors.New("lufact: zero pivot")
)

// Factor factors the row-major n*n matrix a into A = L*U where L is unit
// lower triangular (diagonal exactly 1) and U is upper triangular.
//
// Doolittle, no pivoting: for k = 0..n-2 the pivot a[k][k] must be non-zero;
// for each i > k, L[i][k] = a[i][k]/a[k][k] and row i is updated in place as
// row_i -= L[i][k]*row_k. The input slice is never modified.
func Factor(a []float64, n int) (L, U []float64, err error) {
	if n < 1 {
		return nil, nil, ErrEmpty
	}
	if len(a) != n*n {
		return nil, nil, ErrDimension
	}

	work := make([]float64, n*n)
	copy(work, a)

	L = make([]float64, n*n)
	for i := 0; i < n; i++ {
		L[i*n+i] = 1 // unit diagonal, set before any failure can occur
	}

	for k := 0; k < n-1; k++ {
		pivot := work[k*n+k]
		if pivot == 0 {
			return nil, nil, ErrZeroPivot
		}
		for i := k + 1; i < n; i++ {
			mult := work[i*n+k] / pivot
			L[i*n+k] = mult
			work[i*n+k] = 0 // eliminated entry: U is strictly 0 below its diagonal
			for j := k + 1; j < n; j++ {
				work[i*n+j] -= mult * work[k*n+j]
			}
		}
	}

	return L, work, nil
}
