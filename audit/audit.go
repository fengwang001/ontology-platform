// Package audit 用全量重算的结果与增量维护的视图逐组比对。
package audit

import (
	"fmt"
	"sort"

	"ontology/agg"
	"ontology/change"
	"ontology/view"
)

// Recompute 用独立实现从头折叠整条变更流：Sum/Count 按同一操作
// 序列增量折叠（保证 IEEE754 位级一致），Min/Max/Distinct 由最终
// 成员引用计数导出（与顺序无关）。
func Recompute(changes []change.Change) map[string]agg.Values {
	type rec struct {
		group string
		value float64
	}
	type acc struct {
		count int64
		sum   float64
		refs  map[float64]int
	}
	records := map[uint64]rec{}
	groups := map[string]*acc{}
	link := func(c change.Change) {
		g := groups[*c.Group]
		if g == nil {
			g = &acc{refs: map[float64]int{}}
			groups[*c.Group] = g
		}
		g.count++
		g.sum += c.Value
		g.refs[c.Value]++
		records[c.ID] = rec{group: *c.Group, value: c.Value}
	}
	unlink := func(id uint64) {
		r := records[id]
		g := groups[r.group]
		g.count--
		g.sum -= r.value
		g.refs[r.value]--
		if g.refs[r.value] == 0 {
			delete(g.refs, r.value)
		}
		delete(records, id)
		if g.count == 0 {
			delete(groups, r.group)
		}
	}
	for _, c := range changes {
		_, existed := records[c.ID]
		if existed && (c.Op == change.OpDelete || c.Op == change.OpUpdate) {
			unlink(c.ID)
		}
		if c.Op == change.OpInsert || c.Op == change.OpUpdate {
			link(c)
		}
	}
	out := make(map[string]agg.Values, len(groups))
	for name, g := range groups {
		v := agg.Values{Count: g.count, Sum: g.sum, Distinct: int64(len(g.refs))}
		first := true
		for val := range g.refs {
			if first || val < v.Min {
				v.Min = val
			}
			if first || val > v.Max {
				v.Max = val
			}
			first = false
		}
		out[name] = v
	}
	return out
}

// Compare 逐组逐字段位级比对，返回第一处不一致的可判定错误。
func Compare(got, want map[string]agg.Values) error {
	if len(got) != len(want) {
		return fmt.Errorf("audit: group count %d != %d", len(got), len(want))
	}
	names := make([]string, 0, len(want))
	for name := range want {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		g, ok := got[name]
		if !ok {
			return fmt.Errorf("audit: group %q missing", name)
		}
		if !agg.EqualBits(g, want[name]) {
			return fmt.Errorf("audit: group %q got %+v want %+v",
				name, g, want[name])
		}
	}
	return nil
}

// Check 用 changes 全量重算并与视图当前状态比对。
func Check(v *view.View, changes []change.Change) error {
	return Compare(v.Snapshot(), Recompute(changes))
}
