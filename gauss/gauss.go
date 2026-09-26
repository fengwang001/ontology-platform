// Package gauss inverts n*n matrices via Gauss-Jordan elimination with
// partial pivoting: the augmented matrix [A|I] is reduced to [I|A^-1].
// Every row operation is applied to both halves simultaneously.
package gauss

import (
	"errors"
	"sync/atomic"

	"ontology/pivot"
)

// Sentinel errors; mutually distinguishable via errors.Is.
var (
	ErrEmpty    = errors.New("gauss: empty matrix (n < 1)")
	ErrDim      = errors.New("gauss: len(a) != n*n")
	ErrSingular = errors.New("gauss: singular matrix (zero pivot column)")
)

// mulSubOps counts row multiply-subtract operations (row_i -= m*row_j)
// actually executed by successful Invert calls. Unexported on purpose:
// only same-package white-box tests may observe it.
var mulSubOps atomic.Int64

// Invert returns the inverse of the row-major n*n matrix a.
// a is never modified. Rejected calls leave no trace behind.
func Invert(a []float64, n int) ([]float64, error) {
	if n < 1 {
		return nil, ErrEmpty
	}
	if len(a) != n*n {
		return nil, ErrDim
	}
	L := make([]float64, n*n) // left half, private copy of a
	copy(L, a)
	R := make([]float64, n*n) // right half, identity
	for i := 0; i < n; i++ {
		R[i*n+i] = 1
	}
	ops, err := eliminate(L, R, n)
	if err != nil {
		return nil, err // failure leaves no trace: counter untouched
	}
	mulSubOps.Add(ops)
	return R, nil
}

// eliminate reduces [L|R] to [I|A^-1] in place and returns the number of
// multiply-subtract row operations performed (pivot scans and row
// normalizations are not counted).
func eliminate(L, R []float64, n int) (int64, error) {
	var ops int64
	for k := 0; k < n; k++ {
		p, ok := pivot.Pick(L, n, k)
		if !ok {
			return 0, ErrSingular
		}
		if p != k {
			for j := 0; j < n; j++ {
				L[k*n+j], L[p*n+j] = L[p*n+j], L[k*n+j]
				R[k*n+j], R[p*n+j] = R[p*n+j], R[k*n+j]
			}
		}
		d := L[k*n+k]
		for j := 0; j < n; j++ { // normalize pivot row
			L[k*n+j] /= d
			R[k*n+j] /= d
		}
		for i := k + 1; i < n; i++ { // down: zero column k below
			ops += elimRow(L, R, n, i, k)
		}
		for i := 0; i < k; i++ { // up: zero column k above
			ops += elimRow(L, R, n, i, k)
		}
	}
	return ops, nil
}

// elimRow does row_i -= f*row_k on both halves, where f = L[i][k].
// A zero factor is skipped entirely (no operation, not counted).
func elimRow(L, R []float64, n, i, k int) int64 {
	f := L[i*n+k]
	if f == 0 {
		return 0
	}
	for j := 0; j < n; j++ {
		L[i*n+j] -= f * L[k*n+j]
		R[i*n+j] -= f * R[k*n+j]
	}
	return 1
}
