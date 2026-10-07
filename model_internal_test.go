package ontology

import (
	"errors"
	"testing"
)

func TestRemoveObjectCascades(t *testing.T) {
	g := mkGraph(t)
	addObjs(t, g, "admin", "a", "b")
	must(t, g.AddLink("admin", "ab", "D", "a", "b"))
	must(t, g.AddLink("admin", "ba", "D", "b", "a"))
	must(t, g.RemoveObject("admin", "b"))

	if err := g.RemoveLink("admin", "ab"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("incident link should be gone, got %v", err)
	}
	res, err := g.HasCycle("admin")
	must(t, err)
	if res.HasCycle {
		t.Fatalf("cycle must vanish after endpoint removal: %+v", res)
	}
}

func TestLinkTypeConstraints(t *testing.T) {
	g := NewGraph()
	must(t, g.AddObjectType("T", "T"))
	must(t, g.AddLinkType(LinkType{ID: "strict", Direction: Directed}))
	addObjs(t, g, "admin", "a", "b")
	must(t, g.AddLink("admin", "l1", "strict", "a", "b"))
	// Parallel same-type link between the same ordered pair is rejected.
	if err := g.AddLink("admin", "l2", "strict", "a", "b"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("parallel link: got %v", err)
	}
	// Self loop rejected when disallowed.
	if err := g.AddLink("admin", "l3", "strict", "a", "a"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("self loop: got %v", err)
	}
}

func TestEndpointTypesEnforced(t *testing.T) {
	g := NewGraph()
	must(t, g.AddObjectType("Person", "Person"))
	must(t, g.AddObjectType("Dog", "Dog"))
	must(t, g.AddLinkType(LinkType{
		ID: "owns", Direction: Directed,
		SourceTypeID: "Person", TargetTypeID: "Dog",
	}))
	must(t, g.AddObject("admin", "p", "Person"))
	must(t, g.AddObject("admin", "d", "Dog"))
	must(t, g.AddLink("admin", "ok", "owns", "p", "d"))
	if err := g.AddLink("admin", "bad", "owns", "d", "p"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("reversed endpoint types: got %v", err)
	}
}

func TestPermissionGrantsSurviveRecreation(t *testing.T) {
	g := mkGraph(t)
	must(t, g.GrantExistence("m", "late"))
	addObjs(t, g, "admin", "late")
	res, err := g.HasCycle("m")
	must(t, err)
	if res.HasCycle {
		t.Fatal("single visible object is acyclic")
	}
}
