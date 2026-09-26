// Package api is the public entry point for infinity-norm condition number
// estimation without explicitly forming the inverse. It depends only on
// normest (which in turn depends on norm).
package api

import (
	"errors"

	"ontology/norm"
	"ontology/normest"
)

// errSelfCheck is returned when a built-in invariant check fails.
var errSelfCheck = errors.New("api: self-check failed")

// Estimator estimates κ∞(A) = ‖A‖∞·‖A⁻¹‖∞ with a fixed number of solves.
// It holds no per-call mutable state: Cond is safe for concurrent use on a
// shared estimator and treats its input as read-only.
type Estimator struct {
	k int
}

// New returns an estimator performing k solve rounds. k < 1 is rejected
// with normest.ErrInvalidK before any estimator state exists.
func New(k int) (*Estimator, error) {
	if k < 1 {
		return nil, normest.ErrInvalidK
	}
	return &Estimator{k: k}, nil
}

// Cond estimates κ∞(A) of the n*n invertible row-major matrix a. The input
// slice is never modified. Dimension/empty/singular inputs fail with the
// distinct sentinel errors from the norm/normest packages and leave no
// state behind.
func (e *Estimator) Cond(a []float64, n int) (float64, error) {
	// One LU factorization happens inside EstimateInvInf; it also performs
	// every validation (empty/dimension/singular) before any state change.
	inv, err := normest.EstimateInvInf(a, n, e.k)
	if err != nil {
		return 0, err
	}
	return norm.InfNorm(a, n) * inv, nil // exact ‖A‖∞, read-only
}

// inverseInfNorm is a reference used only by SelfCheck: it explicitly forms
// A⁻¹ (Gauss-Jordan) to compute the true ‖A⁻¹‖∞ for tiny built-in matrices.
// It is never used by Cond or the estimator.
func inverseInfNorm(a []float64, n int) float64 {
	m := make([]float64, n*2*n)
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			m[i*2*n+j] = a[i*n+j]
			m[i*2*n+n+j] = 0
		}
		m[i*2*n+n+i] = 1
	}
	for c := 0; c < n; c++ {
		p := m[c*2*n+c]
		for j := 0; j < 2*n; j++ {
			m[c*2*n+j] /= p
		}
		for r := 0; r < n; r++ {
			if r == c {
				continue
			}
			f := m[r*2*n+c]
			for j := 0; j < 2*n; j++ {
				m[r*2*n+j] -= f * m[c*2*n+j]
			}
		}
	}
	var best float64
	for i := 0; i < n; i++ {
		var s float64
		for j := 0; j < n; j++ {
			v := m[i*2*n+n+j]
			if v < 0 {
				v = -v
			}
			s += v
		}
		if s > best {
			best = s
		}
	}
	return best
}

// SelfCheck verifies on a built-in set of matrices that the estimate is a
// valid lower bound of the true κ∞ (est ≤ true) and not off by more than a
// factor n (est ≥ true/n). The core set is strictly diagonally dominant
// M-matrices (positive diagonal, non-positive off-diagonal entries —
// including explicit zeros and negatives); such matrices have a
// non-negative inverse, so the all-ones round lands on the extremal row and
// the estimate is exact at every k. The worked n=2 example, whose exact
// estimate needs the sign refresh, is added when k ≥ 2.
func (e *Estimator) SelfCheck() error {
	type tc struct {
		a []float64
		n int
	}
	mCases := []tc{
		{[]float64{2, -1, -1, 2}, 2},
		{[]float64{4, -1, -1, -1, 4, -1, -1, -1, 4}, 3},
		{[]float64{3, -1, 0, -1, 3, -2, 0, -2, 5}, 3},
		{[]float64{
			2, 0, 0, 0,
			0, 4, 0, 0,
			0, 0, 8, 0,
			0, 0, 0, 6}, 4},
	}
	if e.k >= 2 { // worked example: exact only after the sign refresh
		mCases = append(mCases, tc{[]float64{1, 3, 0, 2}, 2})
	}
	for _, c := range mCases {
		est, err := e.Cond(c.a, c.n)
		if err != nil {
			return err
		}
		na := norm.InfNorm(c.a, c.n)
		truth := na * inverseInfNorm(c.a, c.n)
		tol := 1e-9 * truth
		if est > truth+tol || est < truth/float64(c.n)-tol {
			return errSelfCheck
		}
	}
	return nil
}
