// Package solve provides forward/backward substitution on an existing LU
// factorization and is the only gateway upper layers use to reach lufact,
// so the dependency stays one-directional: api -> solve -> lufact.
package solve

import "ontology/lufact"

// Factorization errors are re-exported here so api never imports lufact.
var (
	ErrDimension = lufact.ErrDimension // len(a) != n*n
	ErrEmpty     = lufact.ErrEmpty     // n < 1
	ErrZeroPivot = lufact.ErrZeroPivot // zero pivot without pivoting
)

// Factor runs one Doolittle factorization. Upper layers call this rather than
// lufact directly, keeping the package dependency direction one-way.
func Factor(a []float64, n int) (L, U []float64, err error) {
	return lufact.Factor(a, n)
}

// Forward solves L*y = b, computing y[0]..y[n-1] in that order. L is unit
// lower triangular, so y[i] is never divided by L[i][i] (always 1).
// A fresh slice is returned; b is left unchanged.
func Forward(L, b []float64, n int) []float64 {
	y := make([]float64, n)
	for i := 0; i < n; i++ {
		s := b[i]
		for j := 0; j < i; j++ {
			s -= L[i*n+j] * y[j] // b[i] - sum L[i][j]*y[j]
		}
		y[i] = s // no / L[i][i]: it is 1
	}
	return y
}

// Backward solves U*x = y, computing x[n-1]..x[0] in reverse order.
// A fresh slice is returned; y is left unchanged.
func Backward(U, y []float64, n int) []float64 {
	x := make([]float64, n)
	for i := n - 1; i >= 0; i-- {
		s := y[i]
		for j := i + 1; j < n; j++ {
			s -= U[i*n+j] * x[j]
		}
		x[i] = s / U[i*n+i]
	}
	return x
}
