package ontology

import (
	"errors"
	"testing"
)

func TestInvalidCaller(t *testing.T) {
	g := newTestGraph(t)
	for _, bad := range []string{"", "   ", "\t\n"} {
		if _, err := g.HasCycle(bad); !errors.Is(err, ErrInvalidCaller) {
			t.Fatalf("caller %q: want ErrInvalidCaller, got %v", bad, err)
		}
	}
}

func TestEmptyVisibleIsAcyclicNotError(t *testing.T) {
	g := newTestGraph(t)
	addObjs(t, g, "a", "b")
	addLink(t, g, "l1", "dir", "a", "b")
	g.SetPermissions("nobody", Permissions{})
	res, err := g.HasCycle("nobody")
	if err != nil || res.HasCycle || res.Evidence != nil {
		t.Fatalf("empty visible set: got %+v err=%v", res, err)
	}
	grant(g, "sees-a", []string{"a"})
	if res, err := g.HasCycle("sees-a"); err != nil || res.HasCycle {
		t.Fatalf("single visible object no links: %+v err=%v", res, err)
	}
}

func TestSelfLoop(t *testing.T) {
	g := newTestGraph(t)
	addObjs(t, g, "a")
	addLink(t, g, "s1", "dir", "a", "a")
	grantAll(g, "c", "s1")
	res, err := g.HasCycle("c")
	if err != nil || !res.HasCycle || !sameSet(res.Evidence, []string{"a"}) {
		t.Fatalf("self loop: %+v err=%v", res, err)
	}
}

func TestSelfLoopRejectedByType(t *testing.T) {
	g := NewGraph()
	must(t, g.AddObjectType(ObjectType{Name: "Thing"}))
	must(t, g.AddLinkType(LinkType{Name: "noself", Direction: Directed, AllowSelf: false,
		SourceType: "Thing", TargetType: "Thing"}))
	addObjs(t, g, "a")
	err := g.CreateLink(Link{ID: "s1", Type: "noself", Source: "a", Target: "a"})
	if !errors.Is(err, ErrInvalidLink) {
		t.Fatalf("self loop must be rejected, got %v", err)
	}
}

func TestBidirectionalRoundTrip(t *testing.T) {
	g := newTestGraph(t)
	addObjs(t, g, "u", "v")
	addLink(t, g, "b1", "bi", "u", "v")
	grantAll(g, "c", "b1")
	res, err := g.HasCycle("c")
	if err != nil || !res.HasCycle || !sameSet(res.Evidence, []string{"u", "v"}) {
		t.Fatalf("single bidirectional link must be a 2-cycle: %+v err=%v", res, err)
	}
}

func TestTwoOppositeDirectedLinks(t *testing.T) {
	g := newTestGraph(t)
	addObjs(t, g, "u", "v")
	addLink(t, g, "fwd", "dir", "u", "v")
	addLink(t, g, "rev", "dir", "v", "u")
	grantAll(g, "c", "fwd", "rev")
	res, err := g.HasCycle("c")
	if err != nil || !res.HasCycle || !sameSet(res.Evidence, []string{"u", "v"}) {
		t.Fatalf("opposite directed links must form 2-cycle: %+v err=%v", res, err)
	}
}

func TestSingleDirectedLinkIsAcyclic(t *testing.T) {
	g := newTestGraph(t)
	addObjs(t, g, "u", "v")
	addLink(t, g, "fwd", "dir", "u", "v")
	grantAll(g, "c", "fwd")
	if res, err := g.HasCycle("c"); err != nil || res.HasCycle {
		t.Fatalf("one directed link is acyclic: %+v err=%v", res, err)
	}
}
