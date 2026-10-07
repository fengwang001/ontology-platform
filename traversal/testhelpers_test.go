package traversal

import (
	"reflect"
	"testing"
)

func mustAddObjects(t *testing.T, g *Graph, ids ...ObjectID) {
	t.Helper()
	for _, id := range ids {
		if err := g.AddObject(Object{ID: id}); err != nil {
			t.Fatalf("AddObject(%q): %v", id, err)
		}
	}
}

func mustAddLinkTypes(t *testing.T, g *Graph, ts ...LinkTypeID) {
	t.Helper()
	for _, ty := range ts {
		if err := g.AddLinkType(ty); err != nil {
			t.Fatalf("AddLinkType(%q): %v", ty, err)
		}
	}
}

func mustAddLink(t *testing.T, g *Graph, id LinkID, ty LinkTypeID, from, to ObjectID) {
	t.Helper()
	if err := g.AddLink(Link{ID: id, Type: ty, Source: from, Target: to}); err != nil {
		t.Fatalf("AddLink(%q): %v", id, err)
	}
}

// canonicalResult 将结果转为与实现无关的可比较形态。
type pathShape struct {
	nodes  []ObjectID
	links  []LinkID
	status TerminalStatus
	repeat ObjectID
}

func shapes(res *TraversalResult) []pathShape {
	out := make([]pathShape, 0, len(res.Paths))
	for _, p := range res.Paths {
		s := pathShape{nodes: p.Nodes, status: p.Status}
		for _, l := range p.Links {
			s.links = append(s.links, l.LinkID)
		}
		if p.Cycle != nil {
			s.repeat = p.Cycle.RepeatedObject
		}
		out = append(out, s)
	}
	return out
}

func assertResultsEqual(t *testing.T, got, want *TraversalResult) {
	t.Helper()
	if got.SnapshotVersion != want.SnapshotVersion {
		t.Fatalf("snapshot version: got %d want %d", got.SnapshotVersion, want.SnapshotVersion)
	}
	if !reflect.DeepEqual(shapes(got), shapes(want)) {
		t.Fatalf("path mismatch:\n got %#v\nwant %#v", shapes(got), shapes(want))
	}
}

func statusCounts(res *TraversalResult) map[TerminalStatus]int {
	out := map[TerminalStatus]int{}
	for _, p := range res.Paths {
		out[p.Status]++
	}
	return out
}
