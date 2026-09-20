package ontology

import (
	"reflect"
	"testing"
)

func TestUpdateMovesBetweenValues(t *testing.T) {
	s := NewStore("color")
	s.Upsert("e1", map[string]any{"color": "red"})
	s.Upsert("e1", map[string]any{"color": "blue"})

	if got := s.Lookup("color", "red"); len(got) != 0 {
		t.Fatalf("old value still visible: %v", got)
	}
	if got := s.Lookup("color", "blue"); !reflect.DeepEqual(got, []string{"e1"}) {
		t.Fatalf("new value lookup = %v", got)
	}
	if n := s.RowCount(); n != 1 {
		t.Fatalf("RowCount = %d", n)
	}
	if err := s.Verify(); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateToNilAndMissing(t *testing.T) {
	s := NewStore("color")
	s.Upsert("e1", map[string]any{"color": "red"})

	s.Upsert("e1", map[string]any{"color": nil})
	if got := s.Lookup("color", "red"); len(got) != 0 {
		t.Fatalf("nil update left residue: %v", got)
	}
	if info := s.IsNull("color"); !reflect.DeepEqual(info.Nil, []string{"e1"}) {
		t.Fatalf("nil list = %v", info.Nil)
	}

	s.Upsert("e1", map[string]any{"other": 1})
	if info := s.IsNull("color"); !reflect.DeepEqual(info.Missing, []string{"e1"}) {
		t.Fatalf("missing list = %v", info.Missing)
	}
	if info := s.IsNull("color"); len(info.Nil) != 0 {
		t.Fatalf("nil list not cleared: %v", info.Nil)
	}
	if err := s.Verify(); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteLeavesNoResidue(t *testing.T) {
	s := NewStore("color")
	s.Upsert("e1", map[string]any{"color": "red"})
	s.Upsert("e2", map[string]any{"color": nil})
	s.Delete("e1")
	s.Delete("e2")
	s.Delete("ghost")

	if got := s.Lookup("color", "red"); len(got) != 0 {
		t.Fatalf("lookup after delete = %v", got)
	}
	if info := s.IsNull("color"); info.Total() != 0 {
		t.Fatalf("null info after delete = %+v", info)
	}
	if _, entries := s.IndexStats(); entries != 0 {
		t.Fatalf("entries after delete = %d", entries)
	}
	if n := s.RowCount(); n != 0 {
		t.Fatalf("RowCount = %d", n)
	}
	if err := s.Verify(); err != nil {
		t.Fatal(err)
	}
}

func TestRepeatedUpsertDoesNotInflate(t *testing.T) {
	s := NewStore("color")
	for i := 0; i < 100; i++ {
		s.Upsert("e1", map[string]any{"color": "red"})
	}
	if n := s.RowCount(); n != 1 {
		t.Fatalf("RowCount = %d", n)
	}
	if got := s.Lookup("color", "red"); len(got) != 1 {
		t.Fatalf("lookup = %v", got)
	}
	_, entries := s.IndexStats()
	indexed, _, _ := s.Counts("color")
	if entries != 1 || indexed != 1 {
		t.Fatalf("entries=%d indexed=%d", entries, indexed)
	}
}

func TestCallerMapMutationDoesNotLeak(t *testing.T) {
	s := NewStore("color")
	attrs := map[string]any{"color": "red"}
	s.Upsert("e1", attrs)
	attrs["color"] = "blue"
	if got := s.Lookup("color", "red"); !reflect.DeepEqual(got, []string{"e1"}) {
		t.Fatalf("store observed caller mutation: %v", got)
	}
}
