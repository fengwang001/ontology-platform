// Package quant locates approximate quantiles in a gk summary:
// QUERY(φ) returns the v of the first tuple with rmax ≥ φ·n.
package quant

import (
	"errors"
	"sync/atomic"

	"ontology/gk"
)

var (
	// ErrBadPhi reports a φ outside (0,1).
	ErrBadPhi = errors.New("quant: phi out of range (0,1)")
	// ErrEmpty reports a query on an empty summary (n = 0).
	ErrEmpty = errors.New("quant: query on empty summary")
)

// Engine answers quantile queries against a gk summary.
//
// visited records how many tuples the most recent Query touched while
// locating its answer. It is deliberately unexported: no exported
// function or method reveals it, so only same-package tests can read it.
type Engine struct {
	visited atomic.Int64
}

// Query returns the v of the first tuple with rmax ≥ φ·n. A rejected
// query (bad φ, empty summary) changes nothing and returns a sentinel.
func (e *Engine) Query(s *gk.Summary, phi float64) (int64, error) {
	if phi <= 0 || phi >= 1 {
		return 0, ErrBadPhi
	}
	n := s.N()
	if n == 0 {
		return 0, ErrEmpty
	}
	target := phi * float64(n)
	tuples := s.Tuples()
	var rmin, visited int64
	for _, t := range tuples {
		rmin += t.G
		visited++
		if float64(rmin+t.D) >= target {
			e.visited.Store(visited)
			return t.V, nil
		}
	}
	// Unreachable: the last tuple has rmax = n > φn for any φ < 1.
	e.visited.Store(visited)
	return tuples[len(tuples)-1].V, nil
}
