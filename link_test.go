package ontology

import (
	"strings"
	"testing"
)

func TestRegisterLinkTypeValidation(t *testing.T) {
	s := NewStore()
	mustTypes(t, s, "A")
	if err := s.RegisterLinkType(LinkType{Name: "x", Source: "A", Target: "B"}); err == nil {
		t.Fatal("expected error for unknown target type")
	}
	mustTypes(t, s, "B")
	mustLinkType(t, s, LinkType{Name: "x", Source: "A", Target: "B"})
	if err := s.RegisterLinkType(LinkType{Name: "x", Source: "A", Target: "B"}); err == nil {
		t.Fatal("expected error for duplicate link type")
	}
	if err := s.RegisterLinkType(LinkType{Source: "A", Target: "B"}); err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestBidirectionalTraversalMirror(t *testing.T) {
	s := newGraphStore(t)
	mustObjects(t, s, "Person", "p1", "p2", "p3")
	mustLink(t, s, "knows", "p1", "p2")
	mustLink(t, s, "knows", "p1", "p3")

	if got := s.LinksFrom("knows", "p1"); !equalStrings(got, []string{"p2", "p3"}) {
		t.Fatalf("LinksFrom = %v", got)
	}
	if got := s.LinksTo("knows", "p2"); !equalStrings(got, []string{"p1"}) {
		t.Fatalf("LinksTo = %v", got)
	}
	// Every forward link must be reachable backwards and vice versa.
	for _, src := range []string{"p1", "p2", "p3"} {
		for _, dst := range s.LinksFrom("knows", src) {
			found := false
			for _, back := range s.LinksTo("knows", dst) {
				if back == src {
					found = true
				}
			}
			if !found {
				t.Fatalf("orphan link %s -> %s", src, dst)
			}
		}
	}
	if err := s.CheckInvariant(); err != nil {
		t.Fatalf("CheckInvariant: %v", err)
	}
}

func TestDeleteLink(t *testing.T) {
	s := newGraphStore(t)
	mustObjects(t, s, "Person", "p1", "p2")
	mustLink(t, s, "knows", "p1", "p2")
	if err := s.DeleteLink("knows", "p1", "p2"); err != nil {
		t.Fatalf("DeleteLink: %v", err)
	}
	if s.HasLink("knows", "p1", "p2") {
		t.Fatal("link still present after delete")
	}
	if err := s.DeleteLink("knows", "p1", "p2"); err == nil {
		t.Fatal("expected error deleting missing link")
	}
	if err := s.CheckInvariant(); err != nil {
		t.Fatalf("CheckInvariant: %v", err)
	}
}

func TestLinkErrorCarriesContext(t *testing.T) {
	s := newGraphStore(t)
	mustObjects(t, s, "Person", "p1", "p2")
	mustLink(t, s, "knows", "p1", "p2")
	err := s.CreateLink("knows", "p1", "p2")
	le, ok := err.(*LinkError)
	if !ok {
		t.Fatalf("expected *LinkError, got %T", err)
	}
	if le.LinkType != "knows" || le.Source != "p1" || le.Target != "p2" {
		t.Fatalf("error missing context: %+v", le)
	}
	if !strings.Contains(le.Error(), "knows") {
		t.Fatalf("message missing link type: %q", le.Error())
	}
}

func TestSelfReferencingLinkType(t *testing.T) {
	s := NewStore()
	mustTypes(t, s, "Node")
	mustLinkType(t, s, LinkType{
		Name: "edge", Source: "Node", Target: "Node",
		Cardinality: ManyToMany, OnDelete: SetNull,
	})
	mustObjects(t, s, "Node", "n1", "n2")
	mustLink(t, s, "edge", "n1", "n1") // self-link
	mustLink(t, s, "edge", "n1", "n2")
	if got := s.LinksFrom("edge", "n1"); !equalStrings(got, []string{"n1", "n2"}) {
		t.Fatalf("LinksFrom = %v", got)
	}
	if got := s.LinksTo("edge", "n1"); !equalStrings(got, []string{"n1"}) {
		t.Fatalf("LinksTo = %v", got)
	}
	if err := s.CheckInvariant(); err != nil {
		t.Fatalf("CheckInvariant: %v", err)
	}
}
