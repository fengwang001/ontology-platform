// Package api is the public front end for least-squares via the normal
// equations: Factor caches AᵀA once; Solve only recomputes Aᵀb. Stdlib only.
package api

import (
	"errors"
	"math"
	"sync"
	"sync/atomic"

	"ontology/gram"
	"ontology/lsq"
)

// Decidable sentinel errors (use errors.Is). ErrRankDeficient equals
// gram.ErrSingular: a rank-deficient A yields a singular Gram matrix.
var (
	ErrDimMismatch     = errors.New("api: dimension mismatch (len(a)!=m*n or len(b)!=m)")
	ErrUnderdetermined = errors.New("api: underdetermined system (m<n)")
	ErrEmptySystem     = errors.New("api: empty system (m<1 or n<1)")
	ErrNotFactored     = errors.New("api: Factor must succeed before Solve")
	ErrRankDeficient   = gram.ErrSingular
)

// Solver caches one factorization; cached slices are immutable after
// Factor, so Solve is safe for concurrent read-only use.
type Solver struct {
	mu sync.RWMutex
	a  []float64 // private copy of A, used to form Aᵀb in Solve
	g  []float64 // cached Gram matrix AᵀA
	m  int
	n  int

	gramCount atomic.Int64 // counts O(m·n²) Gram computations; white-box tests only
}

// New returns an empty Solver.
func New() *Solver { return &Solver{} }

// Factor validates the input, computes and probes AᵀA, and publishes
// state only after all checks pass: rejected calls leave cache and
// gramCount untouched. The input a is copied, never modified or retained.
func (s *Solver) Factor(a []float64, m, n int) error {
	if m < 1 || n < 1 {
		return ErrEmptySystem
	}
	if m < n {
		return ErrUnderdetermined
	}
	if len(a) != m*n {
		return ErrDimMismatch
	}
	ac := append([]float64(nil), a...)
	g := gram.Gram(ac, m, n)
	// SolveG copies g, so a failed probe mutates nothing published.
	if _, err := lsq.SolveG(g, make([]float64, n), n); err != nil {
		return err // gram.ErrSingular; state and counter untouched
	}
	s.mu.Lock()
	s.a, s.g, s.m, s.n = ac, g, m, n
	s.gramCount.Add(1)
	s.mu.Unlock()
	return nil
}

// Solve returns the least-squares x for b using the cached Gram; only
// Aᵀb is recomputed. Safe for concurrent use after Factor.
func (s *Solver) Solve(b []float64, m int) ([]float64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.g == nil {
		return nil, ErrNotFactored
	}
	if m != s.m || len(b) != m {
		return nil, ErrDimMismatch
	}
	return lsq.SolveG(s.g, gram.AtB(s.a, b, m, s.n), s.n)
}

// SelfCheck verifies the four invariants on built-in problems (small + m=10000) with throwaway Solvers.
func (s *Solver) SelfCheck() error {
	t := New()
	a := []float64{1, 1, 1, 2, 1, 3}
	b := []float64{2, 3, 5}
	if err := t.Factor(a, 3, 2); err != nil {
		return err
	}
	x, err := t.Solve(b, 3)
	if err != nil || !eq(x, []float64{1.0 / 3, 1.5}, 1e-9) {
		return errors.New("api: self-check solve mismatch")
	}
	if t.g[1] != t.g[2] { // invariant 2: G[0][1]==G[1][0] (built-in n=2)
		return errors.New("api: self-check Gram not symmetric")
	}
	// Invariant 1: Aᵀ(Ax−b)==0 for the built-in residual.
	r := []float64{
		a[0]*x[0] + a[1]*x[1] - b[0],
		a[2]*x[0] + a[3]*x[1] - b[1],
		a[4]*x[0] + a[5]*x[1] - b[2],
	}
	for _, v := range gram.AtB(a, r, 3, 2) {
		if math.Abs(v) > 1e-9 {
			return errors.New("api: self-check residual not orthogonal")
		}
	}
	for _, bb := range [][]float64{{1, 1, 1}, {0, 0, 0}, b} { // invariant 3
		if _, err := t.Solve(bb, 3); err != nil || t.gramCount.Load() != 1 {
			return errors.New("api: self-check Gram recomputed")
		}
	}
	big := New() // invariant 3 at scale: m=10000, Gram counted once
	const bm, bn = 10000, 3
	ba := make([]float64, bm*bn)
	for i := 0; i < bm; i++ { // rows [1,t,t²]: a full-rank Vandermonde
		v := float64(i) / bm
		ba[i*bn], ba[i*bn+1], ba[i*bn+2] = 1, v, v*v
	}
	if err := big.Factor(ba, bm, bn); err != nil {
		return err
	}
	for k := 0; k < 3; k++ {
		bb := make([]float64, bm)
		for i := range bb {
			bb[i] = float64((i + k) % 11)
		}
		if _, err := big.Solve(bb, bm); err != nil || big.gramCount.Load() != 1 {
			return errors.New("api: self-check large-m Gram recomputed")
		}
	}
	if err := t.Factor(a, 2, 3); !errors.Is(err, ErrUnderdetermined) { // invariant 4
		return errors.New("api: self-check expected underdetermined error")
	}
	if xx, err := t.Solve(b, 3); err != nil || !eq(xx, x, 0) || t.gramCount.Load() != 1 {
		return errors.New("api: self-check state changed after rejection")
	}
	return nil
}

func eq(x, y []float64, tol float64) bool {
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if math.Abs(x[i]-y[i]) > tol {
			return false
		}
	}
	return true
}
