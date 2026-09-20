package ontology

import "testing"

func insert(t *testing.T, s *Store, id string, props map[string]Value) error {
	t.Helper()
	return s.Insert(id, props)
}

// SQL semantics (default): NULL never conflicts, not even with
// another NULL.
func TestNullDefaultSQLSemantics(t *testing.T) {
	s := New(NormOptions{}, nameConstraint)
	if err := insert(t, s, "r1", map[string]Value{"name": Null()}); err != nil {
		t.Fatalf("first NULL insert: %v", err)
	}
	if err := insert(t, s, "r2", map[string]Value{"name": Null()}); err != nil {
		t.Fatalf("second NULL insert should not conflict: %v", err)
	}
	if err := insert(t, s, "r3", map[string]Value{"name": Str("alice")}); err != nil {
		t.Fatalf("value after NULL should not conflict: %v", err)
	}
	if err := insert(t, s, "r4", map[string]Value{"name": Null()}); err != nil {
		t.Fatalf("NULL after value should not conflict: %v", err)
	}
}

// NullsEqual mode: two NULLs in the same column conflict.
func TestNullsEqualMode(t *testing.T) {
	c := Constraint{Name: "uniq_name", Columns: []string{"name"}, NullsEqual: true}
	s := New(NormOptions{}, c)
	if err := insert(t, s, "r1", map[string]Value{"name": Null()}); err != nil {
		t.Fatalf("first NULL insert: %v", err)
	}
	err := insert(t, s, "r2", map[string]Value{"name": Null()})
	ce, ok := err.(*ConflictError)
	if !ok {
		t.Fatalf("second NULL insert should conflict, got %v", err)
	}
	if ce.ExistingID != "r1" {
		t.Errorf("conflict should point to r1, got %q", ce.ExistingID)
	}
}

// Composite constraint with any NULL column: under SQL semantics
// the whole key sits out of conflict detection.
func TestCompositeWithNullColumn(t *testing.T) {
	c := Constraint{Name: "uniq_ab", Columns: []string{"a", "b"}}
	s := New(NormOptions{}, c)
	props := map[string]Value{"a": Null(), "b": Str("x")}
	if err := insert(t, s, "r1", props); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if err := insert(t, s, "r2", props); err != nil {
		t.Fatalf("same key with NULL column must not conflict: %v", err)
	}
	// Same NULL column, different other column: also fine.
	if err := insert(t, s, "r3", map[string]Value{"a": Null(), "b": Str("y")}); err != nil {
		t.Fatalf("NULL column must not participate: %v", err)
	}
	// Fully non-NULL duplicate still conflicts.
	if err := insert(t, s, "r4", map[string]Value{"a": Str("x"), "b": Str("x")}); err != nil {
		t.Fatalf("non-NULL insert: %v", err)
	}
	if err := insert(t, s, "r5", map[string]Value{"a": Str("x"), "b": Str("x")}); err == nil {
		t.Fatal("duplicate non-NULL composite key should conflict")
	}
}

// A missing property counts as NULL.
func TestMissingPropertyIsNull(t *testing.T) {
	s := New(NormOptions{}, nameConstraint)
	if err := insert(t, s, "r1", map[string]Value{}); err != nil {
		t.Fatalf("insert without the column: %v", err)
	}
	if err := insert(t, s, "r2", map[string]Value{}); err != nil {
		t.Fatalf("missing column behaves as NULL and must not conflict: %v", err)
	}
}

// Empty string and NULL are different things in both modes.
func TestEmptyStringVsNull(t *testing.T) {
	for _, nullsEqual := range []bool{false, true} {
		c := Constraint{Name: "uniq_name", Columns: []string{"name"}, NullsEqual: nullsEqual}
		s := New(NormOptions{}, c)
		if err := insert(t, s, "null1", map[string]Value{"name": Null()}); err != nil {
			t.Fatalf("nullsEqual=%v: NULL insert: %v", nullsEqual, err)
		}
		if err := insert(t, s, "empty1", map[string]Value{"name": Str("")}); err != nil {
			t.Fatalf("nullsEqual=%v: empty string must not conflict with NULL: %v", nullsEqual, err)
		}
		// Two empty strings always conflict.
		if err := insert(t, s, "empty2", map[string]Value{"name": Str("")}); err == nil {
			t.Fatalf("nullsEqual=%v: two empty strings should conflict", nullsEqual)
		}
	}
}
