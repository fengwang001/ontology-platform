// Package glob is the global materialized view of the two-stage
// pre-aggregator. It applies the change records pushed from package local and
// keeps the current per-key value. It depends only on package local.
package glob

import "ontology/local"

// View is the global materialized view. Zero value is not usable; use NewView.
type View struct {
	values map[string]int64
	// recs retains every applied change record in push order, so the view can
	// be rebuilt from an empty view to prove replay equivalence (invariant 3).
	recs []local.Event
}

// NewView creates an empty view.
func NewView() *View {
	return &View{values: map[string]int64{}}
}

// Apply appends the pushed records in order and adds each delta to the key's
// current value.
func (v *View) Apply(changes []local.Event) {
	for _, c := range changes {
		v.recs = append(v.recs, c) // record first: the log is the source of truth
		v.values[c.Key] += c.Delta
	}
}

// Get returns the current value of k and whether k exists in the view.
func (v *View) Get(k string) (int64, bool) {
	x, ok := v.values[k]
	return x, ok
}

// All returns a copy of the current view.
func (v *View) All() map[string]int64 {
	m := make(map[string]int64, len(v.values))
	for k, x := range v.values {
		m[k] = x
	}
	return m
}

// Replay rebuilds the view from an empty map by applying every retained change
// record in push order, proving the stored view equals its change log.
func (v *View) Replay() map[string]int64 {
	m := make(map[string]int64, len(v.values))
	for _, c := range v.recs {
		m[c.Key] += c.Delta
	}
	return m
}
