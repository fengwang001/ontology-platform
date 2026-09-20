package ontology

import (
	"strings"
	"testing"
)

// Stored and returned values are byte-identical to the caller's
// input: original case, whitespace and code points are preserved.
func TestStoredValueUnchanged(t *testing.T) {
	norm := NormOptions{TrimSpace: true, CaseFold: true}
	s := New(norm, nameConstraint)
	original := "  Alice Straße İ "
	if err := s.Insert("r1", map[string]Value{"name": Str(original)}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	rec, ok := s.Get("r1")
	if !ok {
		t.Fatal("record not found")
	}
	got := rec.Props["name"]
	if got.IsNull() || got.String() != original {
		t.Errorf("stored value = %q, want byte-identical %q", got.String(), original)
	}
}

// The conflict report names the constraint, the existing record's
// primary key, the normalized key, and both sides' original values.
func TestConflictReportContents(t *testing.T) {
	norm := NormOptions{TrimSpace: true, CaseFold: true}
	s := New(norm, nameConstraint)
	if err := s.Insert("user-1", map[string]Value{"name": Str("ALICE")}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	err := s.Insert("user-2", map[string]Value{"name": Str("  alice")})
	ce, ok := err.(*ConflictError)
	if !ok {
		t.Fatalf("expected *ConflictError, got %T: %v", err, err)
	}
	if ce.Constraint != "uniq_name" {
		t.Errorf("Constraint = %q, want %q", ce.Constraint, "uniq_name")
	}
	if ce.ExistingID != "user-1" {
		t.Errorf("ExistingID = %q, want %q", ce.ExistingID, "user-1")
	}
	if ce.Key != "alice" {
		t.Errorf("Key = %q, want normalized %q", ce.Key, "alice")
	}
	if len(ce.Incoming) != 1 || ce.Incoming[0].String() != "  alice" {
		t.Errorf("Incoming = %v, want original %q", ce.Incoming, "  alice")
	}
	if len(ce.Existing) != 1 || ce.Existing[0].String() != "ALICE" {
		t.Errorf("Existing = %v, want original %q", ce.Existing, "ALICE")
	}
	msg := ce.Error()
	for _, want := range []string{"uniq_name", "user-1", "alice", "ALICE"} {
		if !strings.Contains(msg, want) {
			t.Errorf("Error() = %q, should mention %q", msg, want)
		}
	}
}

// Composite conflict reports carry every column's original value.
func TestCompositeConflictReport(t *testing.T) {
	c := Constraint{Name: "uniq_name_city", Columns: []string{"name", "city"}}
	s := New(NormOptions{CaseFold: true}, c)
	if err := s.Insert("r1", map[string]Value{"name": Str("Alice"), "city": Str("Berlin")}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	err := s.Insert("r2", map[string]Value{"name": Str("ALICE"), "city": Str("berlin")})
	ce, ok := err.(*ConflictError)
	if !ok {
		t.Fatalf("expected *ConflictError, got %v", err)
	}
	if ce.Key != "alice | berlin" {
		t.Errorf("Key = %q, want %q", ce.Key, "alice | berlin")
	}
	if ce.Existing[0].String() != "Alice" || ce.Existing[1].String() != "Berlin" {
		t.Errorf("Existing = %v", ce.Existing)
	}
	if ce.Incoming[0].String() != "ALICE" || ce.Incoming[1].String() != "berlin" {
		t.Errorf("Incoming = %v", ce.Incoming)
	}
}

// A failed insert leaves the store unchanged.
func TestFailedInsertLeavesStoreUnchanged(t *testing.T) {
	s := New(NormOptions{CaseFold: true}, nameConstraint)
	if err := s.Insert("r1", map[string]Value{"name": Str("Alice")}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := s.Insert("r2", map[string]Value{"name": Str("ALICE")}); err == nil {
		t.Fatal("expected conflict")
	}
	if _, ok := s.Get("r2"); ok {
		t.Error("rejected record must not be stored")
	}
	if err := s.SelfCheck(); err != nil {
		t.Errorf("SelfCheck after rejected insert: %v", err)
	}
}
