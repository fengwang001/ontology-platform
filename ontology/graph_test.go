package ontology

import (
	"fmt"
	"testing"
)

func TestDirectionsAndDeleteAndSnapshot(t *testing.T) {
	g := NewMemGraph()
	for _, id := range []ObjectID{"a", "b"} {
		mustAdd(t, g, id)
	}
	g.DefineLinkType("t")
	mustAddLink(t, g, Link{ID: "e1", Type: "t", Source: "a", Target: "b"})

	outRes, err := TraverseOnSnapshot(g.Snapshot(), TraverseRequest{
		Start: "b", LinkTypes: outOnly("t"), MaxDepth: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(outRes.Paths) != 1 || outRes.Paths[0].Reason != TerminatedBoundary ||
		fmt.Sprint(outRes.Paths[0].Nodes) != "[b]" {
		t.Fatalf("out-direction from b must stay at b: %+v", outRes.Paths)
	}

	inRes, err := TraverseOnSnapshot(g.Snapshot(), TraverseRequest{
		Start: "b", LinkTypes: map[LinkType]Direction{"t": DirectionIn}, MaxDepth: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(inRes.Paths[0].Nodes) != "[b a]" {
		t.Fatalf("in-direction must reach a: %+v", inRes.Paths)
	}

	snapBeforeDelete := g.Snapshot()
	if err := g.DeleteLink("e1"); err != nil {
		t.Fatal(err)
	}

	// Old snapshot keeps the link; the new snapshot does not.
	before, _ := TraverseOnSnapshot(snapBeforeDelete, TraverseRequest{
		Start: "a", LinkTypes: outOnly("t"), MaxDepth: 5,
	})
	if len(before.Paths[0].Nodes) != 2 {
		t.Fatalf("old snapshot must still contain e1: %+v", before.Paths)
	}
	after, _ := TraverseOnSnapshot(g.Snapshot(), TraverseRequest{
		Start: "a", LinkTypes: outOnly("t"), MaxDepth: 5,
	})
	if len(after.Paths[0].Nodes) != 1 {
		t.Fatalf("new snapshot must not contain e1: %+v", after.Paths)
	}
}

func TestGraphValidation(t *testing.T) {
	g := NewMemGraph()
	mustAdd(t, g, "a")
	g.DefineLinkType("t")

	if err := g.AddLink(Link{ID: "x", Type: "t", Source: "a", Target: "ghost"}); err != ErrObjectNotFound {
		t.Fatalf("want ErrObjectNotFound, got %v", err)
	}
	mustAddLink(t, g, Link{ID: "x", Type: "t", Source: "a", Target: "a"})
	if err := g.AddLink(Link{ID: "x", Type: "t", Source: "a", Target: "a"}); err != ErrDuplicateLink {
		t.Fatalf("want ErrDuplicateLink, got %v", err)
	}
	if err := g.AddObject("a"); err != ErrDuplicateObject {
		t.Fatalf("want ErrDuplicateObject, got %v", err)
	}
	if err := g.DeleteLink("missing"); err != ErrLinkNotFound {
		t.Fatalf("want ErrLinkNotFound, got %v", err)
	}
}
