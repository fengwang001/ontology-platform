package ontology

import (
	"errors"
	"testing"
)

// TestRestrictRollbackAndPath: a RESTRICT deep in the cascade chain must
// abort the whole delete with zero effects and a full path.
func TestRestrictRollbackAndPath(t *testing.T) {
	s := newCascadeStore(t)
	mustObjects(t, s, "A", "a1")
	mustObjects(t, s, "B", "b1", "b2")
	mustObjects(t, s, "C", "c1", "c2")
	// a1 cascades to b1 (ab) and would unlink b2 (keep);
	// b1 cascades to c1 (bc); b1 is guarded to c2 (guard is RESTRICT).
	mustLink(t, s, "ab", "a1", "b1")
	mustLink(t, s, "keep", "a1", "b2")
	mustLink(t, s, "bc", "b1", "c1")
	mustLink(t, s, "guard", "b1", "c2")

	err := s.DeleteObject("a1")
	var re *RestrictError
	if !errors.As(err, &re) {
		t.Fatalf("expected *RestrictError, got %v", err)
	}
	if re.LinkType != "guard" || re.Source != "b1" || re.Target != "c2" {
		t.Fatalf("wrong restrict context: %+v", re)
	}
	// Full path from a1 to the restricted link: a1 -ab- b1 -guard- c2.
	want := []PathStep{
		{From: "a1", LinkType: "ab", To: "b1"},
		{From: "b1", LinkType: "guard", To: "c2"},
	}
	if len(re.Path) != len(want) {
		t.Fatalf("path = %v, want %v", re.Path, want)
	}
	for i := range want {
		if re.Path[i] != want[i] {
			t.Fatalf("path[%d] = %+v, want %+v", i, re.Path[i], want[i])
		}
	}
	// Total rollback: every object and link untouched.
	for _, id := range []string{"a1", "b1", "b2", "c1", "c2"} {
		if !s.HasObject(id) {
			t.Fatalf("%s should still exist after rollback", id)
		}
	}
	links := [][3]string{
		{"ab", "a1", "b1"}, {"keep", "a1", "b2"},
		{"bc", "b1", "c1"}, {"guard", "b1", "c2"},
	}
	for _, l := range links {
		if !s.HasLink(l[0], l[1], l[2]) {
			t.Fatalf("link %v should survive rollback", l)
		}
	}
	if err := s.CheckInvariant(); err != nil {
		t.Fatalf("CheckInvariant: %v", err)
	}
}

func TestRestrictDirectPath(t *testing.T) {
	s := newCascadeStore(t)
	mustObjects(t, s, "B", "b1")
	mustObjects(t, s, "C", "c1")
	mustLink(t, s, "guard", "b1", "c1")
	err := s.DeleteObject("b1")
	var re *RestrictError
	if !errors.As(err, &re) {
		t.Fatalf("expected *RestrictError, got %v", err)
	}
	want := PathStep{From: "b1", LinkType: "guard", To: "c1"}
	if len(re.Path) != 1 || re.Path[0] != want {
		t.Fatalf("path = %v", re.Path)
	}
	if !s.HasObject("b1") || !s.HasLink("guard", "b1", "c1") {
		t.Fatal("state changed despite RESTRICT")
	}
}

// TestRestrictInCycleRollback: a cycle of CASCADE links with one RESTRICT
// attached must roll back entirely.
func TestRestrictInCycleRollback(t *testing.T) {
	s := NewStore()
	mustTypes(t, s, "N")
	mustLinkType(t, s, LinkType{Name: "ring", Source: "N", Target: "N",
		Cardinality: ManyToMany, OnDelete: Cascade})
	mustLinkType(t, s, LinkType{Name: "lock", Source: "N", Target: "N",
		Cardinality: ManyToMany, OnDelete: Restrict})
	mustObjects(t, s, "N", "n1", "n2", "n3", "n4")
	mustLink(t, s, "ring", "n1", "n2")
	mustLink(t, s, "ring", "n2", "n3")
	mustLink(t, s, "ring", "n3", "n1")
	mustLink(t, s, "lock", "n3", "n4")

	err := s.DeleteObject("n1")
	var re *RestrictError
	if !errors.As(err, &re) {
		t.Fatalf("expected *RestrictError, got %v", err)
	}
	if re.LinkType != "lock" {
		t.Fatalf("restrict link = %q, want lock", re.LinkType)
	}
	for _, id := range []string{"n1", "n2", "n3", "n4"} {
		if !s.HasObject(id) {
			t.Fatalf("%s should survive rollback", id)
		}
	}
	if err := s.CheckInvariant(); err != nil {
		t.Fatalf("CheckInvariant: %v", err)
	}
}
