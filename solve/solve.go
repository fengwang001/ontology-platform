// Package solve provides forward/backward substitution on a cached LU
// factorization. It depends only on lufact.
package solve

import "ontology/lufact"

// Forward solves L*y = b where L is unit lower triangular (diagonal 1).
// It proceeds in order y[0] -> y[n-1] and never divides by L[i][i] (it is 1).
func Forward(L, b []float64, n int) []float64 {
	y := make([]float64, n)
	for i := 0; i < n; i++ {
		s := b[i]
		for j := 0; j < i; j++ {
			s -= L[i*n+j] * y[j]
		}
		y[i] = s // L[i][i] == 1, so no division
	}
	return y
}

// Backward solves U*x = y where U is upper triangular.
// It proceeds in reverse order x[n-1] -> x[0], dividing by U[i][i].
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

// FactorSolve is a one-shot convenience: factor a once, then solve A*x = b.
// It is also the single bridge that makes this package depend on lufact.
func FactorSolve(a, b []float64, n int) ([]float64, error) {
	L, U, err := lufact.Factor(a, n)
	if err != nil {
		return nil, err
	}
	if len(b) != n {
		return nil, lufact.ErrDimension
	}
	y := Forward(L, b, n)
	return Backward(U, y, n), nil
}
