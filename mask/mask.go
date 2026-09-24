// Package mask executes compiled masking rules over records.
package mask

import (
	"encoding/json"
	"fmt"
	"hash/fnv"

	"ontology/record"
	"ontology/rule"
)

const (
	replacement = "***"
	truncSuffix = "…"
)

// Hash returns the deterministic FNV-1a 64 hex digest of s.
func Hash(s string) string {
	h := fnv.New64a()
	h.Write([]byte(s))
	return fmt.Sprintf("%016x", h.Sum64())
}

// Apply returns a deep-copied, masked record; the input is never mutated.
func Apply(r *record.Record, set *rule.Set) *record.Record {
	out := *r
	out.Fields = maskMap(r.Fields, set, set.Root())
	return &out
}

func maskMap(in map[string]record.Value, set *rule.Set, n *rule.Node) map[string]record.Value {
	if in == nil {
		return nil
	}
	out := make(map[string]record.Value, len(in))
	for k, v := range in {
		action, keep, child, miss := set.Lookup(n, k)
		if !miss {
			out[k] = maskValue(v, action, keep, set)
			continue
		}
		out[k] = descend(v, set, child)
	}
	return out
}

func descend(v record.Value, set *rule.Set, n *rule.Node) record.Value {
	switch v.Kind {
	case record.KMap:
		return record.M(maskMap(v.Map, set, n))
	case record.KArr:
		items := make([]record.Value, len(v.Arr))
		for i, e := range v.Arr {
			items[i] = descend(e, set, n)
		}
		return record.A(items...)
	default:
		if v.Kind == record.KStr {
			if a, keep, ok := set.MatchValue(v.Str); ok {
				return maskValue(v, a, keep, set)
			}
		}
		return v
	}
}

func maskValue(v record.Value, a rule.Action, keep int, set *rule.Set) record.Value {
	switch a {
	case rule.Replace:
		return record.String(replacement)
	case rule.Hash:
		return record.String(hashValue(v))
	default:
		return record.String(truncate(asString(v), keep))
	}
}

func asString(v record.Value) string {
	if v.Kind == record.KStr {
		return v.Str
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

func hashValue(v record.Value) string {
	return Hash(asString(v))
}

func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + truncSuffix
}
