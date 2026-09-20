package ontology

import (
	"reflect"
	"testing"
)

func TestLookupSortedAndIsolated(t *testing.T) {
	s := NewStore("color")
	s.Upsert("c", map[string]any{"color": "red"})
	s.Upsert("a", map[string]any{"color": "red"})
	s.Upsert("b", map[string]any{"color": "red"})
	got := s.Lookup("color", "red")
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Lookup order = %v, want %v", got, want)
	}
	// Mutating the returned slice must not affect the index.
	got[0] = "zzz"
	again := s.Lookup("color", "red")
	if !reflect.DeepEqual(again, want) {
		t.Fatalf("after mutating result, Lookup = %v, want %v", again, want)
	}
}

func TestLookupNonIndexedAttrAndNilValue(t *testing.T) {
	s := NewStore("color")
	s.Upsert("a", map[string]any{"color": "red", "other": "x"})
	if got := s.Lookup("other", "x"); got != nil {
		t.Fatalf("Lookup on non-indexed attr = %v, want nil", got)
	}
	if got := s.Lookup("color", nil); got != nil {
		t.Fatalf("Lookup nil value = %v, want nil", got)
	}
}

func TestUpdateMovesBetweenValues(t *testing.T) {
	s := NewStore("color")
	s.Upsert("e1", map[string]any{"color": "red"})
	s.Upsert("e1", map[string]any{"color": "blue"})
	if got := s.Lookup("color", "red"); len(got) != 0 {
		t.Fatalf("old value still matches: %v", got)
	}
	if got := s.Lookup("color", "blue"); !reflect.DeepEqual(got, []string{"e1"}) {
		t.Fatalf("new value = %v, want [e1]", got)
	}
	if err := s.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestDeleteLeavesNoResidue(t *testing.T) {
	s := NewStore("color", "dept")
	s.Upsert("e1", map[string]any{"color": "red", "dept": "eng"})
	s.Upsert("e2", map[string]any{"color": "red", "dept": "eng"})
	s.Delete("e1")
	if got := s.Lookup("color", "red"); !reflect.DeepEqual(got, []string{"e2"}) {
		t.Fatalf("color=red = %v, want [e2]", got)
	}
	if got := s.Lookup("dept", "eng"); !reflect.DeepEqual(got, []string{"e2"}) {
		t.Fatalf("dept=eng = %v, want [e2]", got)
	}
	s.Delete("e2")
	if got := s.Lookup("color", "red"); len(got) != 0 {
		t.Fatalf("residue in color index: %v", got)
	}
	if got := s.DistinctValues("color"); got != 0 {
		t.Fatalf("DistinctValues = %d, want 0", got)
	}
	if got := s.TotalEntries("color"); got != 0 {
		t.Fatalf("TotalEntries = %d, want 0", got)
	}
	if err := s.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestRepeatedUpsertDoesNotInflate(t *testing.T) {
	s := NewStore("color")
	for i := 0; i < 50; i++ {
		s.Upsert("e1", map[string]any{"color": "red"})
	}
	if got := s.TotalRows(); got != 1 {
		t.Fatalf("TotalRows = %d, want 1", got)
	}
	if got := s.TotalEntries("color"); got != 1 {
		t.Fatalf("TotalEntries = %d, want 1", got)
	}
	if got := s.Lookup("color", "red"); !reflect.DeepEqual(got, []string{"e1"}) {
		t.Fatalf("Lookup = %v, want [e1]", got)
	}
}

func TestEntryCountsAfterChurn(t *testing.T) {
	s := NewStore("color")
	for i := 0; i < 200; i++ {
		id := string(rune('a'+i%26)) + string(rune('0'+i/26))
		s.Upsert(id, map[string]any{"color": []string{"red", "blue", "green"}[i%3]})
	}
	for i := 0; i < 200; i += 2 {
		id := string(rune('a'+i%26)) + string(rune('0'+i/26))
		s.Upsert(id, map[string]any{"color": "black"})
	}
	for i := 3; i < 200; i += 7 {
		id := string(rune('a'+i%26)) + string(rune('0'+i/26))
		s.Delete(id)
	}
	if got, want := s.TotalEntries("color"), s.IndexedRowCount("color"); got != want {
		t.Fatalf("TotalEntries = %d, IndexedRowCount = %d", got, want)
	}
	if err := s.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestEmptyStringIsNormalValue(t *testing.T) {
	s := NewStore("color")
	s.Upsert("e1", map[string]any{"color": ""})
	if got := s.Lookup("color", ""); !reflect.DeepEqual(got, []string{"e1"}) {
		t.Fatalf("Lookup empty string = %v, want [e1]", got)
	}
	missing, nilIDs := s.IsNull("color")
	if len(missing) != 0 || len(nilIDs) != 0 {
		t.Fatalf("empty string treated as null: missing=%v nil=%v", missing, nilIDs)
	}
	if got := s.IndexedRowCount("color"); got != 1 {
		t.Fatalf("IndexedRowCount = %d, want 1", got)
	}
}
