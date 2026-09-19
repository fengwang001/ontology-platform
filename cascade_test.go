package ontology

import "testing"

// newCascadeStore builds types A,B,C and link types:
// "ab": A->B CASCADE, "bc": B->C CASCADE (chain), "keep": A->B SET_NULL,
// "guard": B->C RESTRICT.
func newCascadeStore(t *testing.T) *Store {
	t.Helper()
	s := NewStore()
	mustTypes(t, s, "A", "B", "C")
	mustLinkType(t, s, LinkType{Name: "ab", Source: "A", Target: "B",
		Cardinality: ManyToMany, OnDelete: Cascade})
	mustLinkType(t, s, LinkType{Name: "bc", Source: "B", Target: "C",
		Cardinality: ManyToMany, OnDelete: Cascade})
	mustLinkType(t, s, LinkType{Name: "keep", Source: "A", Target: "B",
		Cardinality: ManyToMany, OnDelete: SetNull})
	mustLinkType(t, s, LinkType{Name: "guard", Source: "B", Target: "C",
		Cardinality: ManyToMany, OnDelete: Restrict})
	return s
}

func TestSetNullKeepsPeer(t *testing.T) {
	s := newCascadeStore(t)
	mustObjects(t, s, "A", "a1")
	mustObjects(t, s, "B", "b1")
	mustLink(t, s, "keep", "a1", "b1")
	if err := s.DeleteObject("a1"); err != nil {
		t.Fatalf("DeleteObject: %v", err)
	}
	if s.HasObject("a1") {
		t.Fatal("a1 should be deleted")
	}
	if !s.HasObject("b1") {
		t.Fatal("b1 should survive SET_NULL")
	}
	if s.HasLink("keep", "a1", "b1") {
		t.Fatal("link should be gone")
	}
	if err := s.CheckInvariant(); err != nil {
		t.Fatalf("CheckInvariant: %v", err)
	}
}

func TestCascadeRecursive(t *testing.T) {
	s := newCascadeStore(t)
	mustObjects(t, s, "A", "a1")
	mustObjects(t, s, "B", "b1")
	mustObjects(t, s, "C", "c1")
	mustLink(t, s, "ab", "a1", "b1")
	mustLink(t, s, "bc", "b1", "c1")
	if err := s.DeleteObject("a1"); err != nil {
		t.Fatalf("DeleteObject: %v", err)
	}
	for _, id := range []string{"a1", "b1", "c1"} {
		if s.HasObject(id) {
			t.Fatalf("%s should be cascade-deleted", id)
		}
	}
	if err := s.CheckInvariant(); err != nil {
		t.Fatalf("CheckInvariant: %v", err)
	}
}

func TestDeleteMissingObject(t *testing.T) {
	s := newCascadeStore(t)
	if err := s.DeleteObject("ghost"); err == nil {
		t.Fatal("expected error deleting missing object")
	}
}
