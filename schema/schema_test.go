package schema

import (
	"fmt"
	"testing"

	"ontology/field"
)

// TestCheckCountIndependentOfSchemaSize proves fields are located by hash
// index, not by scanning the schema: validating a 1-field event against an
// m-field schema type-checks a number of fields bounded by a small constant
// plus the event's own field count, for m from 100 to 10000.
func TestCheckCountIndependentOfSchemaSize(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		defs := make([]field.Field, m)
		for i := range defs {
			d := field.IntVal(0)
			defs[i] = field.Field{Name: fmt.Sprintf("f%d", i), Type: field.Int, Default: &d}
		}
		s, err := New(defs)
		if err != nil {
			t.Fatalf("m=%d: New: %v", m, err)
		}
		rec, err := s.Validate(map[string]field.Value{"f7": field.IntVal(1)})
		if err != nil {
			t.Fatalf("m=%d: Validate: %v", m, err)
		}
		if len(rec) != m {
			t.Fatalf("m=%d: normalized record has %d fields, want %d", m, len(rec), m)
		}
		if got := s.checked.Load(); got > 2 {
			t.Errorf("m=%d: checked=%d grows with schema size, want <= 1 event field + 1", m, got)
		}
		// A rejected Validate must leave no trace: counter stays put.
		if _, err := s.Validate(map[string]field.Value{"f7": field.StrVal("x")}); err != field.ErrTypeMismatch {
			t.Fatalf("m=%d: want ErrTypeMismatch, got %v", m, err)
		}
		if got := s.checked.Load(); got > 2 {
			t.Errorf("m=%d: rejected Validate changed the counter to %d", m, got)
		}
	}
}
