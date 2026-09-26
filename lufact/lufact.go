// Package lufact performs a single Doolittle LU factorization without pivoting.
package lufact

import "errors"

// Sentinel errors. All four error kinds across the project are distinct.
var (
	// ErrDimension means len(a) != n*n.
	ErrDimension = errors.New("lufact: matrix dimension mismatch")
	// ErrEmpty means n < 1.
	ErrEmpty = errors.New("lufact: empty system")
	// ErrZeroPivot means a zero pivot was hit (no pivoting is performed).
	ErrZeroPivot = errors.New("lufact: zero pivot")
)

// Factor decomposes the row-major n*n matrix a into L (unit lower triangular,
// diagonal fixed to 1) and U (upper triangular) such that L*U == a.
// The input slice is never modified; on error L and U are nil.
func Factor(a []float64, n int) (L, U []float64, err error) {
	if n < 1 {
		return nil, nil, ErrEmpty
	}
	if len(a) != n*n {
		return nil, nil, ErrDimension
	}

	// Work on a private copy so the caller's matrix is untouched.
	w := make([]float64, n*n)
	copy(w, a)

	l := make([]float64, n*n)
	for i := 0; i < n; i++ {
		l[i*n+i] = 1 // unit diagonal; everything else stays 0 for now
	}

	for k := 0; k < n-1; k++ {
		pivot := w[k*n+k]
		if pivot == 0 {
			return nil, nil, ErrZeroPivot
		}
		for i := k + 1; i < n; i++ {
			mik := w[i*n+k] / pivot
			l[i*n+k] = mik // multiplier lives below the L diagonal
			for j := k; j < n; j++ {
				w[i*n+j] -= mik * w[k*n+j] // row_i -= L[i][k] * row_k
			}
		}
	}
	// A final zero pivot on the last diagonal is still a singular system.
	if w[(n-1)*n+(n-1)] == 0 {
		return nil, nil, ErrZeroPivot
	}

	u := make([]float64, n*n)
	for i := 0; i < n; i++ {
		for j := i; j < n; j++ {
			u[i*n+j] = w[i*n+j] // upper triangle incl. diagonal of U
		}
	}
	return l, u, nil
}
