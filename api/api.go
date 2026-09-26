// Package api is the outward-facing entry point: factor once, solve many times.
// It depends on solve (and transitively on lufact); the dependency direction is
// one-way. The factorization counter is intentionally unexported.
package api

import (
	"errors"
	"sync"
	"sync/atomic"

	"ontology/lufact"
	"ontology/solve"
)

// Mutually distinct sentinel errors, usable with errors.Is.
var (
	ErrDimension   = errors.New("api: dimension mismatch")
	ErrEmpty       = errors.New("api: empty system (n < 1)")
	ErrZeroPivot   = errors.New("api: zero pivot")
	ErrNotFactored = errors.New("api: solve called before a successful factor")
)

// Engine caches one factorization. After Factor, Solve only reads the cache.
type Engine struct {
	mu sync.RWMutex
	n  int
	L  []float64
	U  []float64

	// factorCount counts O(n^3) factorizations actually performed. It is
	// unexported; Solve must never cause it to move.
	factorCount atomic.Int64
}

// New returns an empty engine.
func New() *Engine { return &Engine{} }

// Factor validates the input, computes L and U into locals, and only then
// swaps the cache and bumps the counter. A rejected call changes nothing.
func (e *Engine) Factor(a []float64, n int) error {
	if n < 1 {
		return ErrEmpty
	}
	if len(a) != n*n {
		return ErrDimension
	}
	L, U, err := lufact.Factor(a, n)
	if err != nil {
		switch {
		case errors.Is(err, lufact.ErrZeroPivot):
			return ErrZeroPivot
		case errors.Is(err, lufact.ErrEmpty):
			return ErrEmpty
		default:
			return ErrDimension
		}
	}
	e.mu.Lock()
	e.n, e.L, e.U = n, L, U
	e.factorCount.Add(1)
	e.mu.Unlock()
	return nil
}

// Solve reuses the cached factorization to solve A*x = b. It takes only a read
// lock, so many goroutines may solve concurrently against one factorization.
func (e *Engine) Solve(b []float64, n int) ([]float64, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.L == nil {
		return nil, ErrNotFactored
	}
	if n != e.n || len(b) != n {
		return nil, ErrDimension
	}
	y := solve.Forward(e.L, b, n)
	return solve.Backward(e.U, y, n), nil
}
