package view

import (
	"sort"

	"ontology/agg"
)

// Groups returns a snapshot keyed by group name. Groups that lost their
// last member are absent entirely; there are never zero-member entries.
func (v *View) Groups() map[string]GroupResult {
	v.mu.RLock()
	defer v.mu.RUnlock()
	out := make(map[string]GroupResult, len(v.groups))
	for name, g := range v.groups {
		out[name] = snapshot(g)
	}
	return out
}

// GroupNames returns the sorted snapshot of live group names.
func (v *View) GroupNames() []string {
	v.mu.RLock()
	defer v.mu.RUnlock()
	names := make([]string, 0, len(v.groups))
	for name := range v.groups {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Lookup returns one group's snapshot. exists is false when the group has
// no surviving members; callers can distinguish that from a zero result.
func (v *View) Lookup(name string) (GroupResult, bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	g := v.groups[name]
	if g == nil {
		return GroupResult{}, false
	}
	return snapshot(g), true
}

// MaxVersion returns the greatest applied version (0 on an empty view).
func (v *View) MaxVersion() uint64 {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.maxVersion
}

// Stats returns a copy of the maintenance counters.
func (v *View) Stats() Stats {
	v.mu.RLock()
	defer v.mu.RUnlock()
	s := Stats{
		Rejected:       v.stats.Rejected,
		Reapplied:      v.stats.Reapplied,
		TriggersByKind: copyCounts(v.stats.TriggersByKind),
		MembersByKind:  copyCounts(v.stats.MembersByKind),
	}
	return s
}

func copyCounts(in map[agg.Kind]int64) map[agg.Kind]int64 {
	out := make(map[agg.Kind]int64, len(in))
	for k, n := range in {
		out[k] = n
	}
	return out
}

func snapshot(g *groupState) GroupResult {
	var r GroupResult
	r.Count, r.CountExists = g.aggs[agg.Count].Value()
	r.Sum, r.SumExists = g.aggs[agg.Sum].Value()
	r.Min, r.MinExists = g.aggs[agg.Min].Value()
	r.Max, r.MaxExists = g.aggs[agg.Max].Value()
	r.Distinct, r.DistinctExists = g.aggs[agg.DistinctCount].Value()
	return r
}
