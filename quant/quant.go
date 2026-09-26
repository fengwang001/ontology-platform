// Package quant locates φ-quantiles on a gk summary. It depends only on gk.
package quant

import (
	"sync/atomic"

	"ontology/gk"
)

// Engine answers quantile queries against a summary.
type Engine struct {
	visited atomic.Int64 // tuples touched by the last Query; never exported
}

// Query returns v of the first tuple (v ascending) with rmax ≥ φ·n,
// where rmax_i = (Σ_{j≤i} g_j) + Δ_i. The summary must be non-empty.
func (e *Engine) Query(s *gk.Summary, phi float64) int64 {
	target := phi * float64(s.N())
	var rmin, seen, v int64
	for _, t := range s.Tuples() {
		rmin += t.G
		seen++
		v = t.V
		if float64(rmin+t.D) >= target {
			break
		}
	}
	e.visited.Store(seen)
	return v
}
