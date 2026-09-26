// Package norm provides the infinity row-sum norm and a single LU
// factorization (Doolittle, no inverse). It depends on no other package.
package norm

import "errors"

// Sentinel errors. All four failure classes of the project are distinct;
// this package owns the dimension, emptiness and singularity classes.
var (
	// ErrEmpty is returned when n < 1.
	ErrEmpty = errors.New("norm: empty matrix (n < 1)")
	// ErrDim is returned when len(a) != n*n.
	ErrDim = errors.New("norm: dimension mismatch (len(a) != n*n)")
	// ErrSingular is returned when LU hits an exactly-zero pivot.
	ErrSingular = errors.New("norm: singular matrix (zero pivot)")
)

// InfNorm returns ||M||∞ = max_i Σ_j |M[i][j]| for the row-major n×n
// matrix a. Dimensions are assumed already validated by the caller.
func InfNorm(a []float64, n int) float64 {
	best := 0.0
	for i := 0; i < n; i++ {
		sum := 0.0
		row := i * n
		for j := 0; j < n; j++ {
			sum += abs(a[row+j])
		}
		if sum > best {
			best = sum
		}
	}
	return best
}

// Factor computes the Doolittle LU decomposition A = L·U of the row-major
// n×n matrix a, with L unit lower triangular and U upper triangular. The
// input slice is copied first and never modified. A zero pivot means the
// matrix is singular and ErrSingular is returned.
func Factor(a []float64, n int) (L, U []float64, err error) {
	if n < 1 {
		return nil, nil, ErrEmpty
	}
	if len(a) != n*n {
		return nil, nil, ErrDim
	}
	L = make([]float64, n*n)
	U = make([]float64, n*n)
	for i := 0; i < n; i++ {
		L[i*n+i] = 1
	}
	// nzRow[i] holds the columns p < i for which L[i][p] != 0, so
	// provably-zero multiply-adds are skipped: structured inputs
	// (diagonal/triangular/banded) factor in O(n²), dense stay O(n³),
	// with identical values and zero-pivot semantics.
	nzRow := make([][]int, n)
	for i := 0; i < n; i++ {
		ps := nzRow[i]
		// Row i of U: U[i][j] for j >= i.
		for j := i; j < n; j++ {
			s := a[i*n+j]
			for _, p := range ps {
				s -= L[i*n+p] * U[p*n+j]
			}
			U[i*n+j] = s
		}
		if U[i*n+i] == 0 {
			return nil, nil, ErrSingular
		}
		// Column i of L below the diagonal.
		for j := i + 1; j < n; j++ {
			s := a[j*n+i]
			for _, p := range nzRow[j] {
				s -= L[j*n+p] * U[p*n+i]
			}
			v := s / U[i*n+i]
			L[j*n+i] = v
			if v != 0 {
				nzRow[j] = append(nzRow[j], i)
			}
		}
	}
	return L, U, nil
}

// Solve solves A·x = b given L, U from Factor, using forward substitution
// on L·z = b followed by back substitution on U·x = z. It never forms A⁻¹.
func Solve(L, U []float64, n int, b []float64) []float64 {
	z := make([]float64, n)
	for i := 0; i < n; i++ { // L unit lower triangular.
		s := b[i]
		for j := 0; j < i; j++ {
			s -= L[i*n+j] * z[j]
		}
		z[i] = s
	}
	x := make([]float64, n)
	for i := n - 1; i >= 0; i-- {
		s := z[i]
		for j := i + 1; j < n; j++ {
			s -= U[i*n+j] * x[j]
		}
		x[i] = s / U[i*n+i]
	}
	return x
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
