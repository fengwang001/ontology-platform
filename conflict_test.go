package ontology

import "testing"

// TestConflictReport verifies the error pinpoints the constraint, the
// existing record's pk, the normalized key, and both sides' raw values.
func TestConflictReport(t *testing.T) {
	s := NewStore(
		Normalize{TrimSpace: true, CaseFold: true},
		Constraint{Name: "uq_name", Cols: []string{"name"}},
		Constraint{Name: "uq_email", Cols: []string{"email"}},
	)
	mustNoErr(t, s.Insert("pk1", map[string]Value{
		"name":  Str("ALICE"),
		"email": Str("a@example.com"),
	}))
	err := s.Insert("pk2", map[string]Value{
		"name":  Str("  alice"),
		"email": Str("b@example.com"),
	})
	ce := mustConflict(t, err)
	if ce.Constraint != "uq_name" {
		t.Errorf("Constraint=%q, want uq_name", ce.Constraint)
	}
	if ce.ExistingPK != "pk1" {
		t.Errorf("ExistingPK=%q, want pk1", ce.ExistingPK)
	}
	if want := "V5:alice"; ce.Key != want {
		t.Errorf("Key=%q, want normalized %q", ce.Key, want)
	}
	if got := ce.Incoming["name"].Str; got != "  alice" {
		t.Errorf("Incoming raw=%q, want caller's original %q", got, "  alice")
	}
	if got := ce.Existing["name"].Str; got != "ALICE" {
		t.Errorf("Existing raw=%q, want stored original %q", got, "ALICE")
	}
	// The email constraint must still be independently reported.
	ce2 := mustConflict(t, s.Insert("pk3", map[string]Value{
		"name":  Str("bob"),
		"email": Str("A@EXAMPLE.COM"),
	}))
	if ce2.Constraint != "uq_email" || ce2.ExistingPK != "pk1" {
		t.Errorf("email conflict misreported: %+v", ce2)
	}
}

// TestCompositeConflictKey checks the normalized key of a composite
// constraint and that both columns' raw values are reported.
func TestCompositeConflictKey(t *testing.T) {
	s := NewStore(
		Normalize{TrimSpace: true, CaseFold: true},
		Constraint{Name: "uq_fl", Cols: []string{"first", "last"}},
	)
	mustNoErr(t, s.Insert("p1", rec("first", "Ada", "last", "Lovelace")))
	ce := mustConflict(t, s.Insert("p2", rec("first", " ADA ", "last", "lovelace")))
	if want := "V3:adaV8:lovelace"; ce.Key != want {
		t.Errorf("Key=%q, want %q", ce.Key, want)
	}
	if ce.Incoming["first"].Str != " ADA " || ce.Existing["first"].Str != "Ada" {
		t.Errorf("raw values not preserved in report: %+v", ce)
	}
}
