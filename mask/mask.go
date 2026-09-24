// Package mask applies a compiled rule.Set to records: fields are deep-copied
// and sensitive string leaves are replaced/hashed/truncated.
package mask

import (
	"crypto/sha256"
	"encoding/hex"

	"ontology/record"
	"ontology/rule"
)

// Replacement is the fixed replacement token (length deliberately not kept).
const Replacement = "***"

// Masker binds a compiled rule set to the masking operation.
type Masker struct {
	set *rule.Set
}

// New returns a Masker for the compiled set.
func New(set *rule.Set) *Masker { return &Masker{set: set} }

// Set exposes the compiled rules (used for complexity accounting).
func (m *Masker) Set() *rule.Set { return m.set }

// Apply returns a masked deep copy; the input record is never mutated.
func (m *Masker) Apply(r *record.Record) (*record.Record, error) {
	if err := record.Validate(r.Fields); err != nil {
		return nil, err
	}
	out := *r
	out.Fields = m.clone(r.Fields, nil).(record.Fields)
	return &out, nil
}

func (m *Masker) clone(v any, path []record.Seg) any {
	if len(path) > record.MaxDepth {
		return v
	}
	switch t := v.(type) {
	case record.Fields:
		out := make(record.Fields, len(t))
		for k, child := range t {
			childPath := appendSeg(path, record.Seg{Key: k, Index: -1})
			out[k] = m.clone(child, childPath)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, child := range t {
			childPath := appendSeg(path, record.Seg{Key: k, Index: -1})
			out[k] = m.clone(child, childPath)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, child := range t {
			childPath := appendSeg(path, record.Seg{Index: i})
			out[i] = m.clone(child, childPath)
		}
		return out
	case string:
		return m.maskString(path, t)
	default:
		return v
	}
}

func appendSeg(path []record.Seg, s record.Seg) []record.Seg {
	out := make([]record.Seg, 0, len(path)+1)
	out = append(out, path...)
	return append(out, s)
}

func (m *Masker) maskString(path []record.Seg, v string) string {
	if a, ok := m.set.Lookup(path); ok {
		return Transform(v, a)
	}
	if a, ok := m.set.MatchPattern(v); ok {
		return Transform(v, a)
	}
	return v
}

// Transform performs one action on a scalar value.
func Transform(v string, a rule.BoundAction) string {
	switch a.Action {
	case rule.Replace:
		return Replacement
	case rule.Hash:
		sum := sha256.Sum256([]byte(v))
		return hex.EncodeToString(sum[:])
	case rule.Truncate:
		runes := []rune(v)
		if len(runes) <= a.Keep {
			return v
		}
		if a.Keep <= 0 {
			return "\u2026(truncated)"
		}
		return string(runes[:a.Keep]) + "\u2026(truncated)"
	}
	return v
}
