// Package api is the outward face of the CDC runtime schema validator.
package api

import (
	"errors"
	"fmt"
	"maps"

	"ontology/field"
	"ontology/schema"
)

type Field = field.Field
type Value = field.Value

const (
	Int  = field.Int
	Str  = field.Str
	Bool = field.Bool
)

var IntVal, StrVal, BoolVal = field.IntVal, field.StrVal, field.BoolVal

var (
	ErrEmptyFieldName     = field.ErrEmptyFieldName
	ErrDuplicateField     = field.ErrDuplicateField
	ErrRequiredHasDefault = field.ErrRequiredHasDefault
	ErrOptionalNoDefault  = field.ErrOptionalNoDefault
	ErrDefaultType        = field.ErrDefaultType
	ErrMissingRequired    = field.ErrMissingRequired
	ErrTypeMismatch       = field.ErrTypeMismatch
)

// Validator validates CDC events against one registered schema.
type Validator struct{ s *schema.Schema }

// New registers an ordered field list; illegal definitions fail wholesale.
func New(fields []Field) (*Validator, error) {
	s, err := schema.New(fields)
	if err != nil {
		return nil, err
	}
	return &Validator{s: s}, nil
}

// Validate returns the normalized record or nil plus a decidable error.
func (v *Validator) Validate(ev map[string]Value) (map[string]Value, error) {
	return v.s.Validate(ev)
}

// specFields is the task's schema: id/name required, score/active optional.
func specFields() []Field {
	zero, off := IntVal(0), BoolVal(false)
	return []Field{
		{Name: "id", Type: Int, Required: true},
		{Name: "name", Type: Str, Required: true},
		{Name: "score", Type: Int, Default: &zero},
		{Name: "active", Type: Bool, Default: &off},
	}
}

// naive is the per-field reference implementation of the five rules.
func naive(fields []Field, ev map[string]Value) (map[string]Value, error) {
	out := make(map[string]Value, len(fields))
	for _, f := range fields {
		v, ok := ev[f.Name]
		if !ok {
			if f.Required {
				return nil, ErrMissingRequired
			}
			v = *f.Default
		} else if v.Kind != f.Type {
			return nil, ErrTypeMismatch
		}
		out[f.Name] = v
	}
	return out, nil
}

// genEvent deterministically derives an event from i: random-ish field
// omissions, unknown fields, and type mismatches.
func genEvent(i int) map[string]Value {
	ev := map[string]Value{}
	put := func(c bool, k string, v Value) {
		if c {
			ev[k] = v
		}
	}
	put(i%2 == 0, "id", IntVal(int64(i)))
	put(i%3 == 0, "name", StrVal("n"))
	put(i%5 == 0, "score", IntVal(int64(i)))
	put(i%7 == 0, "active", BoolVal(true))
	put(i%4 == 0, "junk", IntVal(1))  // unknown field
	put(i%11 == 0, "name", IntVal(9)) // type mismatch
	return ev
}

// SelfCheck verifies the four invariants against a built-in event sequence.
// It only builds its own validators, so it is safe to call concurrently.
func (v *Validator) SelfCheck() error {
	good := map[string]Value{"id": IntVal(1), "name": StrVal("a")}
	before, err := v.Validate(good)
	if err != nil {
		return fmt.Errorf("selfcheck: baseline: %w", err)
	}
	for i, ev := range []map[string]Value{
		{"id": IntVal(2)},                    // missing required name
		{"id": IntVal(3), "name": IntVal(4)}, // type mismatch on name
	} {
		if out, err := v.Validate(ev); err == nil || out != nil {
			return fmt.Errorf("selfcheck: reject %d must yield nil record and error", i)
		}
	}
	after, err := v.Validate(good)
	if err != nil || !maps.Equal(before, after) {
		return errors.New("selfcheck: state changed after rejection")
	}
	sv, err := New(specFields())
	if err != nil {
		return fmt.Errorf("selfcheck: spec schema: %w", err)
	}
	for i := 0; i < 128; i++ { // invariant 1+2: match the naive reference
		ev := genEvent(i)
		got, gerr := sv.Validate(ev)
		want, werr := naive(specFields(), ev)
		if gerr != werr || (gerr == nil && !maps.Equal(got, want)) {
			return fmt.Errorf("selfcheck: diverges from naive on event %d", i)
		}
	}
	// The eight-event sequence from the spec, with expected verdicts.
	steps := []struct {
		ev   map[string]Value
		want error
	}{{map[string]Value{"id": IntVal(1), "name": StrVal("a")}, nil},
		{map[string]Value{"id": IntVal(2), "name": StrVal("b"), "score": IntVal(5)}, nil},
		{map[string]Value{"id": IntVal(3)}, ErrMissingRequired},
		{map[string]Value{"id": IntVal(4), "name": StrVal("c"), "active": BoolVal(true)}, nil},
		{map[string]Value{"id": IntVal(5), "name": StrVal("d"), "extra": IntVal(123)}, nil},
		{map[string]Value{"id": IntVal(6), "name": IntVal(7)}, ErrTypeMismatch},
		{map[string]Value{"id": IntVal(7), "name": StrVal("f"), "score": IntVal(9), "active": BoolVal(false)}, nil},
		{map[string]Value{"id": IntVal(8), "name": StrVal("g"), "active": StrVal("yes")}, ErrTypeMismatch},
	}
	for i, st := range steps {
		out, err := sv.Validate(st.ev)
		if err != st.want || (st.want == nil && out == nil) {
			return fmt.Errorf("selfcheck: step %d: got err=%v want %v", i+1, err, st.want)
		}
	}
	return nil
}
