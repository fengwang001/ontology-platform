// Package lsq solves the normal equations G·x = c, where G is the
// symmetric (positive definite when A has full column rank) Gram
// matrix AᵀA. It depends only on the stdlib; api layers sit above it.
package lsq

import "ontology/gram"

// ErrSingular reports rank deficiency: it is the same value as
// gram.ErrSingular, so callers can errors.Is(err, lsq.ErrSingular)
// or errors.Is(err, gram.ErrSingular) interchangeably.
var ErrSingular = gram.ErrSingular

// SolveG returns x satisfying G·x = c via Gaussian elimination without
// pivoting followed by back-substitution, treating the full n×n matrix.
// Neither g nor c is modified: the elimination runs on a private copy,
// so a cached Gram matrix stays reusable across many right-hand sides.
func SolveG(g, c []float64, n int) ([]float64, error) {
	u := make([]float64, n*n)
	copy(u, g)
	d := make([]float64, n)
	copy(d, c)

	for k := 0; k < n; k++ {
		piv := u[k*n+k]
		// No pivoting is allowed; a zero (or vanishing) diagonal means
		// the symmetric matrix is not positive definite, hence singular.
		if piv == 0 || (k > 0 && abs(piv) < 1e-12*abs(u[0])) {
			return nil, ErrSingular
		}
		for i := k + 1; i < n; i++ {
			f := u[i*n+k] / piv
			if f == 0 {
				continue
			}
			for j := k; j < n; j++ {
				u[i*n+j] -= f * u[k*n+j]
			}
			d[i] -= f * d[k]
		}
	}

	x := make([]float64, n)
	for i := n - 1; i >= 0; i-- {
		s := d[i]
		for j := i + 1; j < n; j++ {
			s -= u[i*n+j] * x[j] // full symmetric upper triangle is used
		}
		x[i] = s / u[i*n+i]
	}
	return x, nil
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
