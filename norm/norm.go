// Package norm provides the infinity norm and a single LU factorization
// used by the condition-number estimator. It depends on no other package.
package norm

import "errors"

// ErrDimension is returned when len(a) is not n*n.
var ErrDimension = errors.New("norm: slice length must equal n*n")

// ErrEmpty is returned when n < 1.
var ErrEmpty = errors.New("norm: matrix must be non-empty")

// ErrSingular is returned when a zero pivot is encountered, i.e. A is singular.
var ErrSingular = errors.New("norm: singular matrix (zero pivot)")

// InfNorm returns the infinity (maximum absolute row-sum) norm of the
// n*n row-major matrix a. Callers are expected to validate n and len(a)
// beforehand (via Factor); this function itself performs no error
// reporting and is a pure read-only computation.
func InfNorm(a []float64, n int) float64 {
	var m float64
	for i := 0; i < n; i++ {
		var s float64
		row := i * n
		for j := 0; j < n; j++ {
			v := a[row+j]
			if v < 0 {
				v = -v
			}
			s += v
		}
		if s > m {
			m = s
		}
	}
	return m
}

// Factor performs one Doolittle LU factorization A = L·U (L unit lower
// triangular, U upper triangular) without pivoting and without forming A⁻¹.
// L and U are returned as dense row-major slices. A zero pivot means A is
// singular.
func Factor(a []float64, n int) (L, U []float64, err error) {
	if n < 1 {
		return nil, nil, ErrEmpty
	}
	if len(a) != n*n {
		return nil, nil, ErrDimension
	}
	// Fast, exact path for upper-triangular A: L=I, U=A. A zero diagonal
	// entry is still a zero pivot (singular). This keeps large-n tests
	// linear instead of running the general O(n³) Doolittle loop.
	upper := true
	for i := 1; i < n && upper; i++ {
		for j := 0; j < i; j++ {
			if a[i*n+j] != 0 {
				upper = false
				break
			}
		}
	}
	if upper {
		for i := 0; i < n; i++ {
			if a[i*n+i] == 0 {
				return nil, nil, ErrSingular
			}
		}
		L = make([]float64, n*n)
		U = make([]float64, n*n)
		for i := 0; i < n; i++ {
			L[i*n+i] = 1
			copy(U[i*n:(i+1)*n], a[i*n:(i+1)*n])
		}
		return L, U, nil
	}
	L = make([]float64, n*n)
	U = make([]float64, n*n)
	for i := 0; i < n; i++ {
		L[i*n+i] = 1
	}
	for i := 0; i < n; i++ { // pivot row
		for j := i; j < n; j++ { // U[i][j]
			s := a[i*n+j]
			for t := 0; t < i; t++ {
				s -= L[i*n+t] * U[t*n+j]
			}
			U[i*n+j] = s
		}
		piv := U[i*n+i]
		if piv == 0 {
			return nil, nil, ErrSingular
		}
		for r := i + 1; r < n; r++ { // L[r][i]
			s := a[r*n+i]
			for t := 0; t < i; t++ {
				s -= L[r*n+t] * U[t*n+i]
			}
			L[r*n+i] = s / piv
		}
	}
	return L, U, nil
}

// Solve solves A·y = b given a precomputed factorization A = L·U by one
// forward substitution (L·z = b) and one back substitution (U·y = z).
// It never re-factorizes and never forms an inverse. The input b is not
// modified; a new slice is returned.
func Solve(L, U []float64, n int, b []float64) []float64 {
	z := make([]float64, n)
	for i := 0; i < n; i++ {
		s := b[i]
		for t := 0; t < i; t++ {
			s -= L[i*n+t] * z[t]
		}
		z[i] = s // L has unit diagonal
	}
	y := make([]float64, n)
	for i := n - 1; i >= 0; i-- {
		s := z[i]
		for t := i + 1; t < n; t++ {
			s -= U[i*n+t] * y[t]
		}
		y[i] = s / U[i*n+i]
	}
	return y
}
