// Package mask 按编译好的规则集对记录字段执行脱敏。
package mask

import (
	"encoding/hex"
	"hash/fnv"
	"strconv"

	"ontology/record"
	"ontology/rule"
)

const truncatedSuffix = "...<truncated>"

// Apply 在记录的字段深拷贝上脱敏，返回是否发生了替换。
func Apply(r *record.Record, set *rule.Set) bool {
	cp := deepCopy(r.Fields)
	fm, _ := cp.(map[string]any)
	changed := walk(fm, nil, set)
	r.Fields = fm
	return changed
}

func walk(v any, segs []string, set *rule.Set) bool {
	switch t := v.(type) {
	case map[string]any:
		changed := false
		for k, child := range t {
			path := append(append([]string{}, segs...), k)
			var act *rule.Rule
			if a := set.Lookup(path); a != nil {
				act = a
			}
			t[k] = transformNode(child, path, set, act, &changed)
		}
		return changed
	case []any:
		changed := false
		for i, child := range t {
			path := append(append([]string{}, segs...), strconv.Itoa(i))
			var act *rule.Rule
			if a := set.Lookup(path); a != nil {
				act = a
			}
			t[i] = transformNode(child, path, set, act, &changed)
		}
		return changed
	default:
		return false
	}
}

func transformNode(child any, path []string, set *rule.Set,
	pathAct *rule.Rule, changed *bool) any {
	if s, ok := child.(string); ok {
		act := pathAct
		if act == nil {
			act = set.LookupValue(s)
		}
		if act != nil {
			*changed = true
			return Transform(s, act)
		}
	}
	if walk(child, path, set) {
		*changed = true
	}
	return child
}

// Transform 按单条规则转换一个标量值。
func Transform(v string, r *rule.Rule) string {
	switch r.Method {
	case rule.Replace:
		return r.Replace
	case rule.Hash:
		h := fnv.New64a()
		h.Write([]byte(v))
		return "h:" + hex.EncodeToString(h.Sum(nil))
	case rule.Truncate:
		runes := []rune(v)
		if r.Keep <= 0 {
			return truncatedSuffix
		}
		if r.Keep >= len(runes) {
			return v
		}
		return string(runes[:r.Keep]) + truncatedSuffix
	default:
		return v
	}
}

func deepCopy(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, c := range t {
			out[k] = deepCopy(c)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, c := range t {
			out[i] = deepCopy(c)
		}
		return out
	default:
		return v
	}
}
