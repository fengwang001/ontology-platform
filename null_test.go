package ontology

import "testing"

// TestNullSQLSemantics: by default NULL never conflicts, not even
// with another NULL.
func TestNullSQLSemantics(t *testing.T) {
	s := nameStore(Normalize{})
	mustNoErr(t, s.Insert("a", nullRec("name")))
	mustNoErr(t, s.Insert("b", nullRec("name"))) // NULL vs NULL: no conflict
	mustNoErr(t, s.Insert("c", rec("name", "x")))
	mustNoErr(t, s.Insert("d", nullRec("name"))) // NULL vs value: no conflict
	if s.Len() != 4 {
		t.Fatalf("Len=%d, want 4", s.Len())
	}
}

// TestNullsEqualMode: with NullsEqual, two NULL rows clash.
func TestNullsEqualMode(t *testing.T) {
	s := NewStore(Normalize{},
		Constraint{Name: "uq_name", Cols: []string{"name"}, NullsEqual: true})
	mustNoErr(t, s.Insert("a", nullRec("name")))
	ce := mustConflict(t, s.Insert("b", nullRec("name")))
	if ce.Constraint != "uq_name" || ce.ExistingPK != "a" {
		t.Fatalf("unexpected conflict detail: %+v", ce)
	}
	if !ce.Incoming["name"].Null || !ce.Existing["name"].Null {
		t.Fatalf("both sides should be NULL: %+v", ce)
	}
}

// TestCompositeWithNull: under SQL semantics a single NULL column
// excludes the whole composite constraint from conflict detection.
func TestCompositeWithNull(t *testing.T) {
	cols := []string{"a", "b"}

	sql := NewStore(Normalize{}, Constraint{Name: "uq", Cols: cols})
	mustNoErr(t, sql.Insert("r1", map[string]Value{"a": Str("x"), "b": Null()}))
	// Same "a", also NULL "b": must NOT conflict in default mode.
	mustNoErr(t, sql.Insert("r2", map[string]Value{"a": Str("x"), "b": Null()}))
	// Same "a", non-NULL "b" equal to another row's: still no conflict
	// with the NULL rows, but conflicts with an equal non-NULL row.
	mustNoErr(t, sql.Insert("r3", map[string]Value{"a": Str("x"), "b": Str("y")}))
	mustConflict(t, sql.Insert("r4", map[string]Value{"a": Str("x"), "b": Str("y")}))

	eq := NewStore(Normalize{},
		Constraint{Name: "uq", Cols: cols, NullsEqual: true})
	mustNoErr(t, eq.Insert("r1", map[string]Value{"a": Str("x"), "b": Null()}))
	// NullsEqual: both NULL in "b" with equal "a" -> conflict.
	mustConflict(t, eq.Insert("r2", map[string]Value{"a": Str("x"), "b": Null()}))
}

// TestEmptyStringVsNull: "" and NULL are distinct in every mode.
func TestEmptyStringVsNull(t *testing.T) {
	for _, nullsEqual := range []bool{false, true} {
		s := NewStore(Normalize{}, Constraint{
			Name: "uq_name", Cols: []string{"name"}, NullsEqual: nullsEqual})
		mustNoErr(t, s.Insert("empty", rec("name", "")))
		// NULL never equals "" even in NullsEqual mode.
		mustNoErr(t, s.Insert("null", nullRec("name")))
		// But "" conflicts with "".
		mustConflict(t, s.Insert("empty2", rec("name", "")))
	}
}
