// Package mask executes a compiled rule set against a record, producing
// a masked deep copy. The input record is never mutated.
package mask

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"

	"ontology/record"
	"ontology/rule"
)

// DefaultReplacement is used when a replace rule has an empty Param.
const DefaultReplacement = "***"

// TruncatedMark annotates values shortened by a truncate rule.
const TruncatedMark = "…(truncated)"

// Apply returns a masked deep copy of rec according to set.
func Apply(rec *record.Record, set *rule.Set) *record.Record {
	out := record.Clone(rec)
	walkMap(out.Fields, nil, set)
	return out
}

func walkMap(m map[string]any, path []string, set *rule.Set) {
	for k, v := range m {
		segs := append(path[:len(path):len(path)], k)
		switch t := v.(type) {
		case map[string]any:
			walkMap(t, segs, set)
		case []any:
			walkSlice(t, segs, set)
		case string:
			m[k] = maskString(t, segs, set)
		default:
			if action, param, ok := set.Lookup(rule.Join(segs)); ok {
				m[k] = applyAction(action, param, t)
			}
		}
	}
}

func walkSlice(s []any, path []string, set *rule.Set) {
	for i, v := range s {
		switch t := v.(type) {
		case map[string]any:
			walkMap(t, path, set)
		case []any:
			walkSlice(t, path, set)
		case string:
			s[i] = maskString(t, path, set)
		}
	}
}

func maskString(s string, segs []string, set *rule.Set) any {
	if action, param, ok := set.Lookup(rule.Join(segs)); ok {
		return applyAction(action, param, s)
	}
	for _, p := range set.Patterns() {
		if p.Re.MatchString(s) {
			return applyAction(p.Action, p.Param, s)
		}
	}
	return s
}

func applyAction(action rule.Action, param string, v any) any {
	switch action {
	case rule.ActionReplace:
		if param == "" {
			return DefaultReplacement
		}
		return param
	case rule.ActionHash:
		sum := sha256.Sum256([]byte(asString(v)))
		return "sha256:" + hex.EncodeToString(sum[:])
	case rule.ActionTruncate:
		n, err := strconv.Atoi(param)
		if err != nil || n < 0 {
			n = 0
		}
		runes := []rune(asString(v))
		if len(runes) <= n {
			return string(runes)
		}
		return string(runes[:n]) + TruncatedMark
	}
	return v
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, _ := json.Marshal(v)
	return string(b)
}
