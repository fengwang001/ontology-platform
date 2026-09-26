// Package api is the public entry point for infinity-norm condition
// number estimation. It depends only on normest (which depends on norm).
package api

import (
	"errors"

	"ontology/norm"
	"ontology/normest"
)

// The four failure classes are distinct sentinels, re-exported from their
// defining packages so callers can judge them with errors.Is.
var (
	ErrEmpty    = norm.ErrEmpty    // n < 1
	ErrDim      = norm.ErrDim      // len(a) != n*n
	ErrSingular = norm.ErrSingular // zero pivot during LU
	ErrBadK     = normest.ErrBadK  // k < 1
)

// Estimator holds the fixed iteration count k. It is safe for concurrent
// use: Cond reads only its input and all shared counters are atomic.
type Estimator struct {
	k int
}

// New creates an Estimator performing k solve rounds. k < 1 is rejected
// with ErrBadK before any state is created.
func New(k int) (*Estimator, error) {
	if k < 1 {
		return nil, ErrBadK
	}
	return &Estimator{k: k}, nil
}

// Cond estimates κ∞(A) = ||A||∞ · est||A⁻¹||∞. The input slice is read
// only; LU is performed once inside the estimator. All failures leave
// estimator state untouched.
func (e *Estimator) Cond(a []float64, n int) (float64, error) {
	if n < 1 {
		return 0, ErrEmpty
	}
	if len(a) != n*n {
		return 0, ErrDim
	}
	inv, err := normest.EstimateInvInf(a, n, e.k)
	if err != nil {
		return 0, err
	}
	return norm.InfNorm(a, n) * inv, nil
}

// SelfCheck verifies the four invariants on built-in matrices:
// counter semantics and the round trace via normest.SelfCheck (invariants
// 2 and 3), the lower-bound estimate against hand-computed true κ∞
// (invariant 1), and distinct sentinels with continued usability after
// rejection (invariant 4).
func (e *Estimator) SelfCheck() error {
	if err := normest.SelfCheck(); err != nil {
		return err
	}
	cases := []struct {
		a    []float64
		n    int
		true float64 // hand-computed true κ∞
	}{
		{[]float64{1, 3, 0, 2}, 2, 10},
		{[]float64{2, 0, 0, 8}, 2, 4},
		{[]float64{-2, 0, 0, -4}, 2, 2},
		{[]float64{4}, 1, 1},
	}
	for _, c := range cases {
		got, err := e.Cond(c.a, c.n)
		if err != nil {
			return err
		}
		if got > c.true*(1+1e-12) || got < c.true/float64(c.n) {
			return errors.New("api: κ estimate outside [true/n, true]")
		}
	}
	sentinels := []error{ErrEmpty, ErrDim, ErrSingular, ErrBadK}
	for i := range sentinels {
		for j := i + 1; j < len(sentinels); j++ {
			if errors.Is(sentinels[i], sentinels[j]) {
				return errors.New("api: sentinel errors not distinct")
			}
		}
	}
	if _, err := e.Cond([]float64{1, 0, 0, 1, 0, 0, 0, 0}, 3); !errors.Is(err, ErrDim) {
		return errors.New("api: dim rejection missing")
	}
	if _, err := e.Cond(cases[0].a, 2); err != nil { // still usable
		return errors.New("api: estimator unusable after rejection")
	}
	return nil
}
