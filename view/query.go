package view

import (
	"sort"

	"ontology/agg"
)

// Groups lists the live group keys in sorted order. A group whose last
// record was deleted does not appear here.
func (v *View) Groups() []string {
	v.mu.RLock()
	defer v.mu.RUnlock()
	out := make([]string, 0, len(v.groups))
	for k := range v.groups {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Group returns one group's aggregates, or false if the group is gone.
func (v *View) Group(key string) (Result, bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	g, ok := v.groups[key]
	if !ok {
		return Result{}, false
	}
	return resultOf(g), true
}

// Snapshot copies every group's aggregates. Callers never observe a
// half-updated group: reads only see post-Commit state.
func (v *View) Snapshot() map[string]Result {
	v.mu.RLock()
	defer v.mu.RUnlock()
	out := make(map[string]Result, len(v.groups))
	for k, g := range v.groups {
		out[k] = resultOf(g)
	}
	return out
}

// Stats returns a copy of the recompute counters.
func (v *View) Stats() Stats {
	v.mu.RLock()
	defer v.mu.RUnlock()
	m := make(map[agg.Kind]int64, len(v.recomputes))
	for k, n := range v.recomputes {
		m[k] = n
	}
	return Stats{Recomputes: m, MemberVisits: v.memberVisits}
}

// Rejected returns the number of refused changes.
func (v *View) Rejected() int64 {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.rejected
}

func resultOf(g *group) Result {
	return Result{Count: g.count, Sum: g.sum, Min: g.min, Max: g.max, Distinct: g.distinct}
}
