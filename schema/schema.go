// Package schema holds an ordered field list and validates whole CDC
// events against it, normalizing accepted events. Depends on field only.
package schema

import (
	"sync/atomic"

	"ontology/field"
)

// Schema is an ordered field list plus a name hash index.
type Schema struct {
	fields []field.Field
	index  map[string]int
	// checked counts schema fields type-checked during the last successful
	// Validate. Unexported on purpose: no exported API may expose it.
	checked atomic.Int64
}

// New validates defs and returns a Schema, or nil plus a decidable error.
// A rejected definition leaves no trace: nothing is shared until success.
func New(defs []field.Field) (*Schema, error) {
	idx := make(map[string]int, len(defs))
	for i, f := range defs {
		if err := field.CheckDef(f); err != nil {
			return nil, err
		}
		if _, dup := idx[f.Name]; dup {
			return nil, field.ErrDuplicateField
		}
		idx[f.Name] = i
	}
	cp := make([]field.Field, len(defs))
	copy(cp, defs)
	return &Schema{fields: cp, index: idx}, nil
}

// Validate checks ev and returns a normalized record covering every schema
// field in schema order, or nil plus a decidable error. Unknown event
// fields are silently dropped. Only event fields found via the hash index
// are type-checked, so `checked` is bounded by len(ev), independent of the
// schema size. Failures mutate only locals and leave no trace.
func (s *Schema) Validate(ev map[string]field.Value) (map[string]field.Value, error) {
	vals := make(map[string]field.Value, len(ev))
	mism := make(map[string]bool, len(ev))
	var checks int64
	for name, v := range ev {
		i, ok := s.index[name]
		if !ok {
			continue // unknown field: silently dropped
		}
		checks++
		if field.CheckValue(s.fields[i], v) != nil {
			mism[name] = true
			continue
		}
		vals[name] = v
	}
	out := make(map[string]field.Value, len(s.fields))
	for _, f := range s.fields { // schema order: first fault decides
		if mism[f.Name] {
			return nil, field.ErrTypeMismatch
		}
		v, ok := vals[f.Name]
		if !ok {
			if f.Required {
				return nil, field.ErrMissingRequired
			}
			v = *f.Default // compatible degradation: fill default
		}
		out[f.Name] = v
	}
	s.checked.Store(checks) // only a successful Validate records anything
	return out, nil
}
