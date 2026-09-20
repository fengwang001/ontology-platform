package ontology

import (
	"reflect"
	"testing"
)

func TestQuerySortedAndConsistentWithScan(t *testing.T) {
	s := New("color")
	s.Upsert("c", map[string]any{"color": "red"})
	s.Upsert("a", map[string]any{"color": "red"})
	s.Upsert("b", map[string]any{"color": "blue"})
	s.Upsert("d", map[string]any{"color": "red"})
	got := s.Query("color", "red")
	want := []string{"a", "c", "d"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Query red = %v, want %v", got, want)
	}
	if got := s.Query("color", "green"); len(got) != 0 {
		t.Fatalf("Query green = %v, want empty", got)
	}
	if got := s.Query("nope", "red"); len(got) != 0 {
		t.Fatalf("Query on unindexed attr = %v, want empty", got)
	}
}

func TestQueryResultIsolatedFromIndex(t *testing.T) {
	s := New("color")
	s.Upsert("a", map[string]any{"color": "red"})
	s.Upsert("b", map[string]any{"color": "red"})
	got := s.Query("color", "red")
	got[0] = "tampered"
	got = append(got, "extra")
	again := s.Query("color", "red")
	if !reflect.DeepEqual(again, []string{"a", "b"}) {
		t.Fatalf("index state leaked into caller slice: %v", again)
	}
}

func TestUpdateMovesIDBetweenValues(t *testing.T) {
	s := New("color")
	s.Upsert("x", map[string]any{"color": "red"})
	s.Upsert("x", map[string]any{"color": "blue"})
	if got := s.Query("color", "red"); len(got) != 0 {
		t.Fatalf("old value still matches: %v", got)
	}
	if got := s.Query("color", "blue"); !reflect.DeepEqual(got, []string{"x"}) {
		t.Fatalf("new value does not match: %v", got)
	}
	if err := s.Verify(); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteLeavesNoResidue(t *testing.T) {
	s := New("color", "size")
	s.Upsert("x", map[string]any{"color": "red", "size": 3})
	s.Upsert("y", map[string]any{"color": "red", "size": 3})
	s.Delete("x")
	if got := s.Query("color", "red"); !reflect.DeepEqual(got, []string{"y"}) {
		t.Fatalf("color index has residue: %v", got)
	}
	if got := s.Query("size", 3); !reflect.DeepEqual(got, []string{"y"}) {
		t.Fatalf("size index has residue: %v", got)
	}
	s.Delete("y")
	if s.TotalEntries() != 0 || s.DistinctValues("color") != 0 {
		t.Fatalf("entries=%d distinct=%d, want 0/0", s.TotalEntries(), s.DistinctValues("color"))
	}
	s.Delete("ghost") // deleting a missing ID must be a no-op
	if err := s.Verify(); err != nil {
		t.Fatal(err)
	}
}

func TestRepeatedUpsertCountsOnce(t *testing.T) {
	s := New("color")
	for i := 0; i < 5; i++ {
		s.Upsert("x", map[string]any{"color": "red"})
	}
	if got := s.Query("color", "red"); !reflect.DeepEqual(got, []string{"x"}) {
		t.Fatalf("duplicate upsert inflated index: %v", got)
	}
	if s.Len() != 1 || s.TotalEntries() != 1 {
		t.Fatalf("Len=%d entries=%d, want 1/1", s.Len(), s.TotalEntries())
	}
}

func TestTotalEntriesMatchesIndexedRows(t *testing.T) {
	s := New("color")
	for i := 0; i < 100; i++ {
		id := string(rune('a'+i%26)) + string(rune('A'+i/26))
		attrs := map[string]any{}
		if i%3 != 0 {
			attrs["color"] = "red"
		}
		s.Upsert(id, attrs)
	}
	indexed, missing, nilVal := s.AttrCounts("color")
	if s.TotalEntries() != indexed {
		t.Fatalf("TotalEntries=%d, indexed rows=%d", s.TotalEntries(), indexed)
	}
	if indexed+missing+nilVal != s.Len() {
		t.Fatalf("counts %d+%d+%d != Len %d", indexed, missing, nilVal, s.Len())
	}
	// Churn: rewrite every entity, then delete half.
	for i := 0; i < 100; i++ {
		id := string(rune('a'+i%26)) + string(rune('A'+i/26))
		s.Upsert(id, map[string]any{"color": "blue"})
	}
	for i := 0; i < 50; i++ {
		id := string(rune('a'+i%26)) + string(rune('A'+i/26))
		s.Delete(id)
	}
	indexed, _, _ = s.AttrCounts("color")
	if s.TotalEntries() != indexed || indexed != 50 {
		t.Fatalf("after churn: entries=%d indexed=%d, want 50/50", s.TotalEntries(), indexed)
	}
	if err := s.Verify(); err != nil {
		t.Fatal(err)
	}
}

func TestDistinctValuesTracked(t *testing.T) {
	s := New("color")
	s.Upsert("a", map[string]any{"color": "red"})
	s.Upsert("b", map[string]any{"color": "blue"})
	s.Upsert("c", map[string]any{"color": "red"})
	if got := s.DistinctValues("color"); got != 2 {
		t.Fatalf("DistinctValues=%d, want 2", got)
	}
	s.Delete("b")
	if got := s.DistinctValues("color"); got != 1 {
		t.Fatalf("DistinctValues after delete=%d, want 1", got)
	}
}
