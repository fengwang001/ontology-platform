// Package quant answers φ-quantile queries against a gk summary by locating
// the first tuple whose rmax = rmin + Δ reaches φ·n.
package quant

import "ontology/gk"

// Querier runs queries against one summary. visited counts how many tuples
// the most recent Query inspected; it is unexported on purpose: no exported
// API may expose it (see the in-package test that reads it directly).
type Querier struct {
	s       *gk.Summary
	visited int
}

// New returns a Querier bound to s.
func New(s *gk.Summary) *Querier { return &Querier{s: s} }

// Query returns the value of the first tuple with rmax ≥ φ·n.
// Callers must guarantee 0 < phi < 1 and s.N() > 0.
func (q *Querier) Query(phi float64) int64 {
	target := phi * float64(q.s.N())
	tuples := q.s.Tuples()
	rmin := 0
	q.visited = 0
	for _, t := range tuples {
		rmin += t.G
		q.visited++
		if float64(rmin+t.Delta) >= target {
			return t.V
		}
	}
	return tuples[len(tuples)-1].V // unreachable for 0<phi<1: last rmax = n
}
