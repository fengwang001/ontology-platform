// Package gram computes the ingredients of the normal equations:
// the symmetric Gram matrix AᵀA and the right-hand side Aᵀb.
//
// Matrices are passed in row-major order: a[k*n+i] is A[k][i].
// The functions never modify their inputs and allocate fresh slices.
package gram

import "errors"

// ErrSingular is the rank-deficiency sentinel: the Gram matrix AᵀA is
// singular exactly when the columns of A are linearly dependent. The
// solver package returns it; callers distinguish it with errors.Is.
var ErrSingular = errors.New("gram: singular Gram matrix (A is rank-deficient)")

// Gram returns the n×n symmetric Gram matrix AᵀA in row-major order.
// Gram[i][j] = Σ_k A[k][i]·A[k][j] is the column dot product of
// columns i and j. The same accumulated value is written to both
// (i,j) and (j,i), so the two are equal bit for bit.
func Gram(a []float64, m, n int) []float64 {
	g := make([]float64, n*n)
	for k := 0; k < m; k++ {
		row := k * n
		for i := 0; i < n; i++ {
			ai := a[row+i]
			for j := i; j < n; j++ {
				v := ai * a[row+j]
				g[i*n+j] += v
				if j != i {
					g[j*n+i] += v
				}
			}
		}
	}
	return g
}

// AtB returns the n-vector Aᵀb, where (Aᵀb)[i] = Σ_k A[k][i]·b[k].
func AtB(a, b []float64, m, n int) []float64 {
	c := make([]float64, n)
	for k := 0; k < m; k++ {
		bk := b[k]
		row := k * n
		for i := 0; i < n; i++ {
			c[i] += a[row+i] * bk
		}
	}
	return c
}
