package view

import (
	"sort"

	"ontology/agg"
)

// Query 返回某组的聚合快照；组不存在时 ok 为 false（不是零值）。
func (v *View) Query(name string) (agg.Values, bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	g := v.groups[name]
	if g == nil {
		return agg.Values{}, false
	}
	return agg.Snapshot(g.aggs), true
}

// Groups 返回当前全部组名（排序后），删空的组不出现。
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

// Snapshot 返回全部组的聚合快照，用于逐字段比对两个视图。
func (v *View) Snapshot() map[string]agg.Values {
	v.mu.RLock()
	defer v.mu.RUnlock()
	out := make(map[string]agg.Values, len(v.groups))
	for k, g := range v.groups {
		out[k] = agg.Snapshot(g.aggs)
	}
	return out
}

// Stats 返回统计快照（重算次数、访问量、拒绝数等）。
func (v *View) Stats() Stats {
	v.mu.RLock()
	defer v.mu.RUnlock()
	s := Stats{
		PerAgg:   make(map[string]AggStats, len(v.stats)),
		Rejected: v.rejected,
		Applied:  v.applied,
	}
	for k, st := range v.stats {
		s.PerAgg[k] = *st
	}
	return s
}

// LastVersion 返回已应用的最大版本号。
func (v *View) LastVersion() uint64 {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.lastVersion
}
