// Package topk tracks candidate keys and reports heavy hitters: every key
// whose true count reaches the threshold is reported (no false negatives);
// extra reports are bounded by the sketch error. See DESIGN.md section 4.
package topk

import (
	"sort"
	"sync/atomic"

	"ontology/bound"
	"ontology/sketch"
)

// Tracker remembers inserted keys and classifies them against a threshold.
// Read-only methods may run concurrently once no more Add calls happen.
type Tracker struct {
	s         *sketch.Sketch
	threshold uint64
	cands     map[string]struct{}
	queries   atomic.Int64 // Estimate calls made by the last HeavyHitters
}

// New builds a tracker over s with the given count threshold.
func New(s *sketch.Sketch, threshold uint64) *Tracker {
	return &Tracker{s: s, threshold: threshold, cands: make(map[string]struct{})}
}

// Threshold returns the configured threshold.
func (t *Tracker) Threshold() uint64 { return t.threshold }

// Add records key as a candidate. Call it after the sketch accepted the add.
func (t *Tracker) Add(key string) { t.cands[key] = struct{}{} }

// Absorb merges other's candidate set into t (used after a sketch merge).
func (t *Tracker) Absorb(o *Tracker) {
	for k := range o.cands {
		t.cands[k] = struct{}{}
	}
}

// HeavyHitters returns every candidate with Estimate >= threshold, sorted.
// Estimate >= true count, so no truly-heavy key is missed.
func (t *Tracker) HeavyHitters() []string {
	t.queries.Store(0)
	var out []string
	for k := range t.cands {
		t.queries.Add(1)
		if est, err := t.s.Estimate(k); err == nil && est >= t.threshold {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// Certain returns the subset that exceeds the threshold even after
// subtracting the nominal error bound: Estimate - eps*N >= threshold.
func (t *Tracker) Certain() []string {
	w, _ := t.s.Dims()
	margin := bound.ErrorBound(w, t.s.Total())
	var out []string
	for k := range t.cands {
		if est, err := t.s.Estimate(k); err == nil && est >= t.threshold+margin {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
