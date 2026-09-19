package ontology

import (
	"fmt"
	"testing"
)

// newRingStore builds a self-referencing CASCADE link type "next" on Node.
func newRingStore(t *testing.T, mode CascadeMode) *Store {
	t.Helper()
	s := NewStore()
	mustTypes(t, s, "Node")
	mustLinkType(t, s, LinkType{
		Name: "next", Source: "Node", Target: "Node",
		Cardinality: ManyToMany, OnDelete: mode,
	})
	return s
}

// TestCascadeCycleTerminates: deleting one node of a CASCADE ring must
// terminate, delete every node exactly once, and leave a clean store.
func TestCascadeCycleTerminates(t *testing.T) {
	s := newRingStore(t, Cascade)
	mustObjects(t, s, "Node", "n1", "n2", "n3", "n4")
	mustLink(t, s, "next", "n1", "n2")
	mustLink(t, s, "next", "n2", "n3")
	mustLink(t, s, "next", "n3", "n4")
	mustLink(t, s, "next", "n4", "n1") // close the ring
	mustLink(t, s, "next", "n1", "n1") // self-loop on top

	if err := s.DeleteObject("n1"); err != nil {
		t.Fatalf("DeleteObject: %v", err)
	}
	for _, id := range []string{"n1", "n2", "n3", "n4"} {
		if s.HasObject(id) {
			t.Fatalf("%s should be deleted exactly once", id)
		}
	}
	if got := s.ObjectsOfType("Node"); len(got) != 0 {
		t.Fatalf("remaining nodes: %v", got)
	}
	if err := s.CheckInvariant(); err != nil {
		t.Fatalf("CheckInvariant: %v", err)
	}
	// Idempotent: deleting again is a plain not-found error, not a crash.
	if err := s.DeleteObject("n1"); err == nil {
		t.Fatal("expected not-found error on second delete")
	}
}

// TestRestrictInCycleRollsBack: a RESTRICT reachable around a ring aborts
// the whole delete.
func TestRestrictInCycleRollsBack(t *testing.T) {
	s := newRingStore(t, Cascade)
	mustLinkType(t, s, LinkType{
		Name: "hold", Source: "Node", Target: "Node",
		Cardinality: ManyToMany, OnDelete: Restrict,
	})
	mustObjects(t, s, "Node", "n1", "n2", "n3")
	mustLink(t, s, "next", "n1", "n2")
	mustLink(t, s, "next", "n2", "n3")
	mustLink(t, s, "next", "n3", "n1")
	mustLink(t, s, "hold", "n3", "n1")

	err := s.DeleteObject("n1")
	if err == nil {
		t.Fatal("expected RESTRICT error")
	}
	for _, id := range []string{"n1", "n2", "n3"} {
		if !s.HasObject(id) {
			t.Fatalf("%s should survive rollback", id)
		}
	}
	if !s.HasLink("next", "n1", "n2") || !s.HasLink("hold", "n3", "n1") {
		t.Fatal("links changed despite RESTRICT")
	}
	if err := s.CheckInvariant(); err != nil {
		t.Fatalf("CheckInvariant: %v", err)
	}
}

// TestFindCyclesDeterministic: results must be stable, sorted, and
// independent of map iteration order.
func TestFindCyclesDeterministic(t *testing.T) {
	build := func() *Store {
		s := newRingStore(t, SetNull)
		mustObjects(t, s, "Node", "a", "b", "c", "d", "e", "x")
		mustLink(t, s, "next", "a", "b")
		mustLink(t, s, "next", "b", "c")
		mustLink(t, s, "next", "c", "a") // cycle a-b-c
		mustLink(t, s, "next", "c", "d")
		mustLink(t, s, "next", "d", "e")
		mustLink(t, s, "next", "e", "c") // cycle c-d-e
		mustLink(t, s, "next", "e", "e") // self-loop
		// x is isolated
		return s
	}
	want := [][]string{{"a", "b", "c"}, {"c", "d", "e"}, {"e"}}
	for i := 0; i < 20; i++ {
		got := build().FindCycles("a", []string{"next"})
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("run %d: FindCycles = %v, want %v", i, got, want)
		}
	}
	// Reachable from the middle of the ring too.
	got := build().FindCycles("d", []string{"next"})
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("FindCycles from d = %v, want %v", got, want)
	}
	// Unknown link type or isolated start yields nothing.
	if got := build().FindCycles("a", []string{"nope"}); got != nil {
		t.Fatalf("FindCycles with unknown type = %v", got)
	}
	if got := build().FindCycles("x", []string{"next"}); got != nil {
		t.Fatalf("FindCycles from isolated node = %v", got)
	}
}

// TestFindCyclesTwoCyclesShareNode: figure-eight graph reports both cycles
// exactly once.
func TestFindCyclesTwoCyclesShareNode(t *testing.T) {
	s := newRingStore(t, SetNull)
	mustObjects(t, s, "Node", "a", "b", "c", "d", "e")
	mustLink(t, s, "next", "a", "b")
	mustLink(t, s, "next", "b", "c")
	mustLink(t, s, "next", "c", "a")
	mustLink(t, s, "next", "a", "d")
	mustLink(t, s, "next", "d", "e")
	mustLink(t, s, "next", "e", "a")
	got := s.FindCycles("a", []string{"next"})
	want := [][]string{{"a", "b", "c"}, {"a", "d", "e"}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("FindCycles = %v, want %v", got, want)
	}
}
