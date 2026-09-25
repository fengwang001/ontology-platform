// Package audit 用全量重算的结果与增量维护的视图逐组比对。
package audit

import (
	"fmt"
	"math"
	"sort"

	"ontology/agg"
	"ontology/view"
)

// Record 是基表一条记录的当前状态。
type Record struct {
	Group string
	Value float64
}

// Full 从记录集合全量重算每组聚合。Sum 用精确求和，与累加顺序无关，
// 因此可与增量维护结果做 IEEE754 位级比对。
func Full(recs map[string]Record) map[string]view.GroupState {
	type acc struct {
		ids      []string
		sum      agg.Summer
		distinct map[float64]struct{}
	}
	groups := map[string]*acc{}
	ids := make([]string, 0, len(recs))
	for id := range recs {
		ids = append(ids, id)
	}
	sort.Strings(ids) // 确定性的累加顺序
	for _, id := range ids {
		r := recs[id]
		a := groups[r.Group]
		if a == nil {
			a = &acc{distinct: map[float64]struct{}{}}
			groups[r.Group] = a
		}
		a.ids = append(a.ids, id)
		a.sum.Add(r.Value)
		a.distinct[r.Value] = struct{}{}
	}
	out := map[string]view.GroupState{}
	for name, a := range groups {
		vals := make([]float64, 0, len(a.ids))
		for _, id := range a.ids {
			vals = append(vals, recs[id].Value)
		}
		min, _ := agg.MinOf(vals)
		max, _ := agg.MaxOf(vals)
		out[name] = view.GroupState{
			Count:    int64(len(a.ids)),
			Sum:      a.sum.Value(),
			Min:      min,
			Max:      max,
			Distinct: int64(len(a.distinct)),
		}
	}
	return out
}

// Compare 逐组比对增量视图与全量重算；Sum 按 Float64bits 位级相等。
func Compare(got, want map[string]view.GroupState) error {
	if len(got) != len(want) {
		return fmt.Errorf("audit: 组数 %d != %d", len(got), len(want))
	}
	for name, w := range want {
		g, ok := got[name]
		if !ok {
			return fmt.Errorf("audit: 组 %q 缺失", name)
		}
		if g.Count != w.Count || g.Min != w.Min || g.Max != w.Max ||
			g.Distinct != w.Distinct ||
			math.Float64bits(g.Sum) != math.Float64bits(w.Sum) {
			return fmt.Errorf("audit: 组 %q 不一致: got %+v want %+v", name, g, w)
		}
	}
	return nil
}
