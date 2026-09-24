// Package merge3 实现三方合并主逻辑：一遍扫描键并集，产出合并结果与冲突清单。
package merge3

import (
	"sort"

	"ontology/conflict"
	"ontology/doc"
)

// Stats 暴露合并过程的可观测计数。
type Stats struct {
	// Lookups 是对三个输入集合的 map 查找总次数（每键 3 次）。
	Lookups int
	// ReportAccesses 是报告生成阶段访问的冲突条数，等于冲突数。
	ReportAccesses int
}

// Merge 合并祖先 anc 与左右两侧快照，返回合并结果、冲突报告与统计。
// 结果与冲突顺序确定：键字典序，同键内字段名字典序。
func Merge(anc, left, right doc.Set) (doc.Set, conflict.Report, Stats) {
	var st Stats
	result := make(doc.Set)
	var collected []conflict.Conflict
	for _, k := range keyUnion(anc, left, right) {
		a, aok := lookup(&st, anc, k)
		l, lok := lookup(&st, left, k)
		r, rok := lookup(&st, right, k)
		mergeKey(k, a, aok, l, lok, r, rok, result, &collected)
	}
	// 报告生成：只访问已收集的冲突，不二次遍历记录集合。
	rep := conflict.Report{List: make([]conflict.Conflict, 0, len(collected))}
	for _, c := range collected {
		st.ReportAccesses++
		rep.Add(c)
	}
	return result, rep, st
}

func lookup(st *Stats, s doc.Set, k string) (doc.Record, bool) {
	st.Lookups++
	r, ok := s[k]
	return r, ok
}

func keyUnion(sets ...doc.Set) []string {
	seen := make(map[string]bool)
	for _, s := range sets {
		for k := range s {
			seen[k] = true
		}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func mergeKey(k string, a doc.Record, aok bool, l doc.Record, lok bool, r doc.Record, rok bool,
	result doc.Set, collected *[]conflict.Conflict) {
	switch {
	case !lok && !rok:
		// 两侧同删（或从未存在）：结果删除，无冲突。
	case !lok:
		mergeOneSideDeleted(k, a, aok, r, true, result, collected)
	case !rok:
		mergeOneSideDeleted(k, a, aok, l, false, result, collected)
	default:
		if !aok {
			// 祖先缺失：视为两侧各自新增。
			if doc.RecordEqual(l, r) {
				result[k] = l
			} else {
				*collected = append(*collected, conflict.Conflict{
					Kind: conflict.AddAdd, Key: k, LeftOK: true, RightOK: true})
				result[k] = l // 确定性规则：取左侧，冲突已上报
			}
			return
		}
		mergeRecord(k, a, l, r, result, collected)
	}
}

// mergeOneSideDeleted 处理一侧删除：另一侧未改则删除生效，已改则 DeleteVsModify 冲突。
func mergeOneSideDeleted(k string, a doc.Record, aok bool, other doc.Record, otherIsRight bool,
	result doc.Set, collected *[]conflict.Conflict) {
	if !aok {
		result[k] = other // 删除侧从未拥有该键，另一侧为新增
		return
	}
	if doc.RecordEqual(a, other) {
		return // 另一侧未改动，删除生效
	}
	c := conflict.Conflict{Kind: conflict.DeleteVsModify, Key: k}
	if otherIsRight {
		c.RightOK = true
	} else {
		c.LeftOK = true
	}
	*collected = append(*collected, c)
	// 确定性规则：删除生效，冲突已上报。
}

// mergeRecord 字段级合并：仅当两侧改同一字段为不同值时才冲突。
func mergeRecord(k string, a, l, r doc.Record, result doc.Set, collected *[]conflict.Conflict) {
	out := doc.Record{}
	for _, f := range fieldUnion(a, l, r) {
		av, aok := a[f]
		lv, lok := l[f]
		rv, rok := r[f]
		lc := changed(av, aok, lv, lok)
		rc := changed(av, aok, rv, rok)
		switch {
		case !lc && !rc:
			if aok {
				out[f] = av
			}
		case lc && !rc:
			if lok {
				out[f] = lv
			}
		case !lc:
			if rok {
				out[f] = rv
			}
		default:
			mergeFieldBothChanged(k, f, lv, lok, rv, rok, out, collected)
		}
	}
	result[k] = out
}

func mergeFieldBothChanged(k, f string, lv doc.Value, lok bool, rv doc.Value, rok bool,
	out doc.Record, collected *[]conflict.Conflict) {
	switch {
	case lok && rok && lv == rv:
		out[f] = lv // 两侧改成相同值：无冲突
	case !lok && !rok:
		// 两侧同删该字段：无冲突
	default:
		kind := conflict.FieldValue
		if lok && rok && lv.Kind != rv.Kind {
			kind = conflict.TypeMismatch
		}
		*collected = append(*collected, conflict.Conflict{
			Kind: kind, Key: k, Field: f,
			Left: lv, LeftOK: lok, Right: rv, RightOK: rok})
		if lok {
			out[f] = lv // 确定性规则：取左侧，冲突已上报
		}
	}
}

func changed(av doc.Value, aok bool, sv doc.Value, sok bool) bool {
	return aok != sok || (aok && av != sv)
}

func fieldUnion(a, l, r doc.Record) []string {
	seen := make(map[string]bool, len(a)+len(l)+len(r))
	for _, rec := range []doc.Record{a, l, r} {
		for f := range rec {
			seen[f] = true
		}
	}
	fields := make([]string, 0, len(seen))
	for f := range seen {
		fields = append(fields, f)
	}
	sort.Strings(fields)
	return fields
}
