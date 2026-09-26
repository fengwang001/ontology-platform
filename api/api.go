// Package api is the public entry point: factor once, solve many RHS.
package api

import (
	"errors"
	"math"
	"sync"
	"sync/atomic"

	"ontology/solve"
)

// ErrNotFactored is the fourth distinct sentinel: Solve before Factor.
var ErrNotFactored = errors.New("api: solve called before a successful factor")

const eps = 1e-9

// Engine caches one factorization; Solve is read-only after Factor.
type Engine struct {
	factorCalls atomic.Int64 // unexported O(n^3) count; same-package tests read it
	mu          sync.RWMutex
	n           int
	L, U        []float64 // immutable once published; Factor replaces wholesale
}

// New returns an empty engine (no factorization yet).
func New() *Engine { return &Engine{} }

// Factor runs one factorization on private slices and publishes the result
// only on success. A rejected call changes no state and does not count.
func (e *Engine) Factor(a []float64, n int) error {
	l, u, err := solve.Factor(a, n)
	if err != nil {
		return err // ran on fresh slices: cache and counter untouched
	}
	e.mu.Lock()
	e.n, e.L, e.U = n, l, u
	e.mu.Unlock()
	e.factorCalls.Add(1) // exactly one O(n^3) factorization happened
	return nil
}

// Solve reuses the cached factorization (forward then backward substitution).
// It never factors and never mutates state; substitutions run lock-free on
// immutable slices, so concurrent Solves are race-free.
func (e *Engine) Solve(b []float64, n int) ([]float64, error) {
	e.mu.RLock()
	n0, l, u := e.n, e.L, e.U
	e.mu.RUnlock()
	if l == nil {
		return nil, ErrNotFactored
	}
	if n != n0 || len(b) != n0 {
		return nil, solve.ErrDimension
	}
	return solve.Backward(u, solve.Forward(l, b, n0), n0), nil
}

// SelfCheck verifies the four invariants on built-in cases.
func (e *Engine) SelfCheck() error {
	cases := []struct {
		a []float64
		n int
	}{
		{[]float64{2, 1, 1, 4, 3, 3, 8, 7, 9}, 3}, // the worked example
		{[]float64{1, 0, -2, 1}, 2},               // zero/negative entries
		{[]float64{-3}, 1},                        // n = 1 edge case
	}
	for _, c := range cases {
		g := New()
		if err := g.Factor(c.a, c.n); err != nil {
			return err
		}
		l, u := g.L, g.U // same package; g is local and never shared
		// Invariants 1 & 2: L*U == A elementwise, unit-L, upper-U.
		for i := 0; i < c.n; i++ {
			if l[i*c.n+i] != 1 {
				return errors.New("SelfCheck: L diagonal not 1")
			}
			for j := 0; j < c.n; j++ {
				s := 0.0
				for k := 0; k < c.n; k++ {
					s += l[i*c.n+k] * u[k*c.n+j]
				}
				if math.Abs(s-c.a[i*c.n+j]) > eps ||
					(j > i && l[i*c.n+j] != 0) || (j < i && u[i*c.n+j] != 0) {
					return errors.New("SelfCheck: reconstruction or triangle violated")
				}
			}
		}
		// Invariant 3: many different b's solved with one factorization.
		p := 1
		for t := 0; t < 20; t++ {
			b := make([]float64, c.n)
			for i := range b {
				p = (p*1103515245 + 12345) & 0x7fffffff
				b[i] = float64(p%17 - 8) // negatives and zeros included
			}
			x, err := g.Solve(b, c.n)
			if err != nil {
				return err
			}
			for i := 0; i < c.n; i++ {
				s := 0.0
				for j := 0; j < c.n; j++ {
					s += c.a[i*c.n+j] * x[j]
				}
				if math.Abs(s-b[i]) > eps {
					return errors.New("SelfCheck: residual too large")
				}
			}
		}
		if g.factorCalls.Load() != 1 {
			return errors.New("SelfCheck: factor count not 1")
		}
	}
	// Four distinct sentinels + invariant 4: rejections leave state usable.
	z := New()
	_, e4 := z.Solve([]float64{1}, 1)
	errs := []error{
		z.Factor(nil, 0),
		z.Factor([]float64{1, 2, 3}, 2),
		z.Factor([]float64{0, 1, 1, 0}, 2),
		e4,
	}
	wants := []error{solve.ErrEmpty, solve.ErrDimension, solve.ErrZeroPivot, ErrNotFactored}
	for i, want := range wants {
		if !errors.Is(errs[i], want) {
			return errors.New("SelfCheck: a sentinel error was not returned")
		}
	}
	if z.factorCalls.Load() != 0 {
		return errors.New("SelfCheck: rejected factor counted")
	}
	g := New()
	if err := g.Factor([]float64{2, 1, 1, 4, 3, 3, 8, 7, 9}, 3); err != nil {
		return err
	}
	if err := g.Factor([]float64{0, 1, 1, 0}, 2); err == nil {
		return errors.New("SelfCheck: bad refactor accepted")
	}
	x, err := g.Solve([]float64{7, 19, 49}, 3)
	if err != nil || math.Abs(x[0]-1) > eps || math.Abs(x[2]-3) > eps {
		return errors.New("SelfCheck: cache altered by rejected factor")
	}
	if g.factorCalls.Load() != 1 {
		return errors.New("SelfCheck: rejected refactor counted")
	}
	return nil
}
