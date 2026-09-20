package ontology

import (
	"reflect"
	"testing"
)

func TestMissingAndNilExcludedFromEqualityIndex(t *testing.T) {
	s := NewStore("dept")
	s.Upsert("has", map[string]any{"dept": "eng"})
	s.Upsert("nil", map[string]any{"dept": nil})
	s.Upsert("gone", map[string]any{"other": 1})
	for _, v := range []any{"eng", "", nil, 0} {
		got := s.Lookup("dept", v)
		for _, id := range got {
			if id == "nil" || id == "gone" {
				t.Fatalf("Lookup(dept,%v) returned %q: %v", v, id, got)
			}
		}
	}
}

func TestIsNullDistinguishesMissingFromNil(t *testing.T) {
	s := NewStore("dept")
	s.Upsert("has", map[string]any{"dept": "eng"})
	s.Upsert("nil1", map[string]any{"dept": nil})
	s.Upsert("nil2", map[string]any{"dept": nil})
	s.Upsert("gone", map[string]any{"other": 1})
	missing, nilIDs := s.IsNull("dept")
	if !reflect.DeepEqual(missing, []string{"gone"}) {
		t.Fatalf("missing = %v, want [gone]", missing)
	}
	if !reflect.DeepEqual(nilIDs, []string{"nil1", "nil2"}) {
		t.Fatalf("nil = %v, want [nil1 nil2]", nilIDs)
	}
	if got := s.NullCount("dept"); got != 2 {
		t.Fatalf("NullCount = %d, want 2", got)
	}
	if got := s.MissingCount("dept"); got != 1 {
		t.Fatalf("MissingCount = %d, want 1", got)
	}
}

func TestThreeWayCountsSumToTotal(t *testing.T) {
	s := NewStore("dept")
	for i := 0; i < 60; i++ {
		id := string(rune('a'+i%26)) + string(rune('0'+i/26))
		switch i % 3 {
		case 0:
			s.Upsert(id, map[string]any{"dept": "eng"})
		case 1:
			s.Upsert(id, map[string]any{"dept": nil})
		default:
			s.Upsert(id, map[string]any{"other": i})
		}
	}
	indexed := s.IndexedRowCount("dept")
	missing := s.MissingCount("dept")
	nils := s.NullCount("dept")
	total := s.TotalRows()
	if indexed+missing+nils != total {
		t.Fatalf("indexed %d + missing %d + nil %d != total %d",
			indexed, missing, nils, total)
	}
	if got := s.TotalEntries("dept"); got != indexed {
		t.Fatalf("TotalEntries = %d, want %d", got, indexed)
	}
}

func TestChangeToNilJoinsIsNull(t *testing.T) {
	s := NewStore("dept")
	s.Upsert("e1", map[string]any{"dept": "eng"})
	s.Upsert("e1", map[string]any{"dept": nil})
	if got := s.Lookup("dept", "eng"); len(got) != 0 {
		t.Fatalf("old value still matches: %v", got)
	}
	_, nilIDs := s.IsNull("dept")
	if !reflect.DeepEqual(nilIDs, []string{"e1"}) {
		t.Fatalf("nil = %v, want [e1]", nilIDs)
	}
	if got := s.TotalEntries("dept"); got != 0 {
		t.Fatalf("TotalEntries = %d, want 0", got)
	}
	// Removing the attribute entirely moves it to "missing".
	s.Upsert("e1", map[string]any{"other": 1})
	missing, nilIDs := s.IsNull("dept")
	if !reflect.DeepEqual(missing, []string{"e1"}) || len(nilIDs) != 0 {
		t.Fatalf("missing = %v nil = %v, want [e1] []", missing, nilIDs)
	}
	if err := s.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestIsNullOnNonIndexedAttr(t *testing.T) {
	s := NewStore("dept")
	s.Upsert("a", map[string]any{"dept": "eng", "note": nil})
	s.Upsert("b", map[string]any{"dept": "eng"})
	missing, nilIDs := s.IsNull("note")
	if !reflect.DeepEqual(missing, []string{"b"}) {
		t.Fatalf("missing = %v, want [b]", missing)
	}
	if !reflect.DeepEqual(nilIDs, []string{"a"}) {
		t.Fatalf("nil = %v, want [a]", nilIDs)
	}
	if got := s.IndexedRowCount("note") + s.MissingCount("note") + s.NullCount("note"); got != 2 {
		t.Fatalf("three-way count = %d, want 2", got)
	}
}
