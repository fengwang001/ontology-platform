package ontology

import (
	"errors"
	"testing"
)

func TestMultiplicityEnforced(t *testing.T) {
	g := NewGraph()
	must(t, g.AddObjectType(ObjectType{Name: "Thing"}))
	must(t, g.AddLinkType(LinkType{Name: "single", Direction: Directed, AllowMulti: false,
		SourceType: "Thing", TargetType: "Thing"}))
	addObjs(t, g, "u", "v")
	must(t, g.CreateLink(Link{ID: "l1", Type: "single", Source: "u", Target: "v"}))
	if err := g.CreateLink(Link{ID: "l2", Type: "single", Source: "u", Target: "v"}); !errors.Is(err, ErrInvalidLink) {
		t.Fatalf("duplicate same-type pair must be rejected, got %v", err)
	}
	must(t, g.AddLinkType(LinkType{Name: "bisingle", Direction: Bidirectional, AllowMulti: false,
		SourceType: "Thing", TargetType: "Thing"}))
	must(t, g.CreateLink(Link{ID: "b1", Type: "bisingle", Source: "u", Target: "v"}))
	err := g.CreateLink(Link{ID: "b2", Type: "bisingle", Source: "v", Target: "u"})
	if !errors.Is(err, ErrInvalidLink) {
		t.Fatalf("reverse-inserted bidirectional duplicate must be rejected, got %v", err)
	}
}

func TestParallelLinksEvidenceDedup(t *testing.T) {
	g := newTestGraph(t)
	addObjs(t, g, "u", "v")
	addLink(t, g, "b1", "bi", "u", "v")
	addLink(t, g, "b2", "bi", "u", "v")
	addLink(t, g, "b3", "bi", "u", "v")
	grantAll(g, "c", "b1", "b2", "b3")
	res, err := g.HasCycle("c")
	if err != nil || !res.HasCycle || len(res.Evidence) != 2 || !sameSet(res.Evidence, []string{"u", "v"}) {
		t.Fatalf("parallel links evidence must be exactly {u,v}: %+v err=%v", res, err)
	}
}

func TestOnlyCycleEdgeExcludedThenRestored(t *testing.T) {
	g := newTestGraph(t)
	addObjs(t, g, "a", "b", "c")
	addLink(t, g, "ab", "dir", "a", "b")
	addLink(t, g, "bc", "dir", "b", "c")
	addLink(t, g, "ca", "dir", "c", "a")

	grantAll(g, "c", "ab", "bc")
	if res, err := g.HasCycle("c"); err != nil || res.HasCycle {
		t.Fatalf("excluded closing edge => no cycle, got %+v err=%v", res, err)
	}

	grantAll(g, "c", "ab", "bc", "ca")
	res, err := g.HasCycle("c")
	if err != nil || !res.HasCycle || !sameSet(res.Evidence, []string{"a", "b", "c"}) {
		t.Fatalf("restored permission => cycle, got %+v err=%v", res, err)
	}
}

func TestExistenceFilterRemovesLinkBeforeTraversal(t *testing.T) {
	g := newTestGraph(t)
	addObjs(t, g, "a", "b")
	addLink(t, g, "ab", "bi", "a", "b")
	grant(g, "c", []string{"a"}, "ab")
	if res, err := g.HasCycle("c"); err != nil || res.HasCycle {
		t.Fatalf("existence-first filter must remove link, got %+v err=%v", res, err)
	}
}

func TestDeleteLinkAffectsNextCall(t *testing.T) {
	g := newTestGraph(t)
	addObjs(t, g, "u", "v")
	addLink(t, g, "b1", "bi", "u", "v")
	grantAll(g, "c", "b1")
	if res, err := g.HasCycle("c"); err != nil || !res.HasCycle {
		t.Fatalf("expected 2-cycle: %+v", res)
	}
	must(t, g.DeleteLink("b1"))
	if res, err := g.HasCycle("c"); err != nil || res.HasCycle {
		t.Fatalf("after delete link, no cycle: %+v err=%v", res, err)
	}
}

func TestCanonicalEvidencePicksShortest(t *testing.T) {
	g := newTestGraph(t)
	addObjs(t, g, "a", "b", "c", "d")
	addLink(t, g, "self", "dir", "a", "a")
	addLink(t, g, "ab", "bi", "a", "b")
	addLink(t, g, "bc", "dir", "b", "c")
	addLink(t, g, "ca", "dir", "c", "a")
	addLink(t, g, "cd", "dir", "c", "d")
	grantAll(g, "c", "self", "ab", "bc", "ca", "cd")
	res, err := g.HasCycle("c")
	if err != nil || !res.HasCycle || !sameSet(res.Evidence, []string{"a"}) {
		t.Fatalf("canonical evidence should be the shortest cycle {a}: %+v", res)
	}
}

func TestTieBreakLexicographic(t *testing.T) {
	g := newTestGraph(t)
	// 两个互不相连的同长 3 环，规范证据取字典序最小的顶点集合。
	addObjs(t, g, "a", "b", "c", "p", "q", "r")
	addLink(t, g, "ab", "dir", "a", "b")
	addLink(t, g, "bc", "dir", "b", "c")
	addLink(t, g, "ca", "dir", "c", "a")
	addLink(t, g, "pq", "dir", "p", "q")
	addLink(t, g, "qr", "dir", "q", "r")
	addLink(t, g, "rp", "dir", "r", "p")
	grantAll(g, "c", "ab", "bc", "ca", "pq", "qr", "rp")
	res, err := g.HasCycle("c")
	if err != nil || !res.HasCycle || !sameSet(res.Evidence, []string{"a", "b", "c"}) {
		t.Fatalf("lexicographically smallest cycle expected, got %+v", res)
	}
}
