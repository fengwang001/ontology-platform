// Package normest estimates ‖A⁻¹‖∞ using a fixed number of linear solves
// against a single LU factorization. It never forms A⁻¹ explicitly.
package normest

import (
	"errors"
	"sync/atomic"

	"ontology/norm"
)

// ErrInvalidK is returned when k < 1.
var ErrInvalidK = errors.New("normest: k must be >= 1")

// solverCalls counts the number of A·y=b solves actually performed. It is
// unexported: only white-box tests in this package may read it; it never
// appears in the public API.
var solverCalls atomic.Int64

// calls returns the cumulative solve count (white-box access only).
func calls() int64 { return solverCalls.Load() }

// vecInf is the vector infinity norm max_i |v[i]|.
func vecInf(v []float64) float64 {
	var m float64
	for _, x := range v {
		if x < 0 {
			x = -x
		}
		if x > m {
			m = x
		}
	}
	return m
}

// EstimateInvInf estimates ‖A⁻¹‖∞ of the n*n invertible row-major matrix a
// with k solves:
//
//	b starts as the all-ones vector; each round solves A·y=b against one
//	reused LU factorization, records ‖y‖∞, then sets b = sign(y)
//	(sign(0)=0). The estimate is the maximum ‖y‖∞ over the k rounds.
//
// Validation runs before any factorization and before the counter is
// touched, so a rejected call changes no state.
func EstimateInvInf(a []float64, n, k int) (float64, error) {
	if k < 1 {
		return 0, ErrInvalidK
	}
	if n < 1 {
		return 0, norm.ErrEmpty
	}
	if len(a) != n*n {
		return 0, norm.ErrDimension
	}
	// One factorization for the whole estimate; never re-done, no inverse.
	L, U, err := norm.Factor(a, n)
	if err != nil { // singular: counter never incremented
		return 0, err
	}
	b := make([]float64, n)
	for i := range b {
		b[i] = 1
	}
	var best float64
	for r := 0; r < k; r++ {
		y := norm.Solve(L, U, n, b) // forward/back substitution only
		solverCalls.Add(1)
		if v := vecInf(y); v > best {
			best = v
		}
		for i := range y { // b = sign(y), sign(0)=0
			switch {
			case y[i] > 0:
				b[i] = 1
			case y[i] < 0:
				b[i] = -1
			default:
				b[i] = 0
			}
		}
	}
	return best, nil
}
