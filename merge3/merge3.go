// Package merge3 实现三方合并主逻辑：一遍扫描三个集合的键并集。
package merge3

import (
	"sort"

	"ontology/conflict"
	"ontology/doc"
)

var lookupCount int

// LookupCount 返回最近一次 Merge 的记录级 map 查找次数。
func LookupCount() int { return lookupCount }

// Merge 合并祖先与左右两侧快照，返回合并结果与冲突清单。
// 冲突在唯一一遍扫描中收集，顺序确定（键、字段字典序）。
func Merge(ancestor, left, right doc.Set) (doc.Set, []conflict.Conflict) {
	lookupCount = 0
	union := make(map[string]bool, len(ancestor))
	for k := range ancestor {
		union[k] = true
	}
	for k := range left {
		union[k] = true
	}
	for k := range right {
		union[k] = true
	}
	keys := make([]string, 0, len(union))
	for k := range union {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	merged := make(doc.Set)
	var conflicts []conflict.Conflict
	for _, k := range keys {
		a, hasA := ancestor[k]
		l, hasL := left[k]
		r, hasR := right[k]
		lookupCount += 3
		rec, cs := mergeKey(k, a, hasA, l, hasL, r, hasR)
		conflicts = append(conflicts, cs...)
		if rec != nil {
			merged[k] = rec
		}
	}
	return merged, conflicts
}

// mergeKey 合并单个键；rec 为 nil 表示结果中该键不存在。
func mergeKey(key string, a doc.Record, hasA bool, l doc.Record, hasL bool, r doc.Record, hasR bool) (doc.Record, []conflict.Conflict) {
	switch {
	case !hasA:
		return mergeAdded(key, l, hasL, r, hasR)
	case !hasL && !hasR:
		return nil, nil // 两侧同删
	case !hasL:
		if doc.Equal(a, r) {
			return nil, nil // 左删右未动
		}
		return nil, []conflict.Conflict{conflict.New(key, "", conflict.DeleteModify, nil, r)}
	case !hasR:
		if doc.Equal(a, l) {
			return nil, nil // 右删左未动
		}
		return nil, []conflict.Conflict{conflict.New(key, "", conflict.DeleteModify, l, nil)}
	}
	return mergeFields(key, a, l, r)
}

// mergeAdded 处理祖先缺失（视为两侧各自新增）的情形。
func mergeAdded(key string, l doc.Record, hasL bool, r doc.Record, hasR bool) (doc.Record, []conflict.Conflict) {
	switch {
	case !hasL && !hasR:
		return nil, nil
	case !hasL:
		return r, nil
	case !hasR:
		return l, nil
	case doc.Equal(l, r):
		return l, nil
	}
	return l, []conflict.Conflict{conflict.New(key, "", conflict.AddAdd, l, r)}
}

// mergeFields 对三方都存在的记录做字段级合并。
func mergeFields(key string, a, l, r doc.Record) (doc.Record, []conflict.Conflict) {
	fields := make(map[string]bool, len(a))
	for f := range a {
		fields[f] = true
	}
	for f := range l {
		fields[f] = true
	}
	for f := range r {
		fields[f] = true
	}
	names := make([]string, 0, len(fields))
	for f := range fields {
		names = append(names, f)
	}
	sort.Strings(names)

	out := make(doc.Record)
	var conflicts []conflict.Conflict
	for _, f := range names {
		av, inA := a[f]
		lv, inL := l[f]
		rv, inR := r[f]
		switch {
		case doc.ValueEqual(lv, rv, inL, inR):
			if inL { // 含两侧同值、两侧同删字段、均未动
				out[f] = lv
			}
		case doc.ValueEqual(av, lv, inA, inL):
			if inR {
				out[f] = rv
			}
		case doc.ValueEqual(av, rv, inA, inR):
			if inL {
				out[f] = lv
			}
		default:
			kind := conflict.FieldValue
			if inL && inR && doc.TypeName(lv) != doc.TypeName(rv) {
				kind = conflict.TypeMismatch
			}
			conflicts = append(conflicts, conflict.New(key, f, kind, lv, rv))
			if inL {
				out[f] = lv // 冲突时结果取左，冲突清单另行报告
			}
		}
	}
	return out, conflicts
}
