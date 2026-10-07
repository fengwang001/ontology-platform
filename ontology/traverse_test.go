package ontology

import (
	"fmt"
	"strings"
	"testing"
)

func outOnly(t LinkType) map[LinkType]Direction {
	return map[LinkType]Direction{t: DirectionOut}
}

func buildGraph(t *testing.T, objects []ObjectID, links []Link) *MemGraph {
	t.Helper()
	g := NewMemGraph()
	for _, o := range objects {
		if err := g.AddObject(o); err != nil {
			t.Fatalf("add object %s: %v", o, err)
		}
	}
	for _, l := range links {
		if err := g.AddLink(l); err != nil {
			t.Fatalf("add link %s: %v", l.ID, err)
		}
	}
	return g
}

func fatal(t *testing.T, format string, args ...any) {
	if t != nil {
		t.Fatalf(format, args...)
	}
	panic(fmt.Sprintf(format, args...))
}

func reasons(res TraverseResult) map[string]TerminalReason {
	out := map[string]TerminalReason{}
	for _, p := range res.Paths {
		out[fmt.Sprint(p.Nodes)] = p.Reason
	}
	return out
}

// A self-loop is a real cycle on the very first hop.
func TestSelfLoopDetectedOnFirstHop(t *testing.T) {
	g := buildGraph(t,
		[]ObjectID{"a"},
		[]Link{{ID: "l1", Type: "t", Source: "a", Target: "a"}},
	)
	g.DefineLinkType("t")
	svc := NewTraverseService(g, nil)

	res, err := svc.Traverse(TraverseRequest{Start: "a", LinkTypes: outOnly("t"), MaxDepth: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Paths) != 1 {
		t.Fatalf("want 1 path, got %d: %+v", len(res.Paths), res.Paths)
	}
	p := res.Paths[0]
	if p.Reason != TerminatedCycle {
		t.Fatalf("self-loop must be a cycle on first hop, got %s", p.Reason)
	}
	if len(p.Nodes) != 2 || p.Nodes[0] != "a" || p.Nodes[1] != "a" {
		t.Fatalf("unexpected path nodes: %v", p.Nodes)
	}
}

// Cycle must beat the depth limit even when both hold on the same hop.
func TestCycleWinsOverDepthLimit(t *testing.T) {
	g := buildGraph(t,
		[]ObjectID{"a"},
		[]Link{{ID: "l1", Type: "t", Source: "a", Target: "a"}},
	)
	g.DefineLinkType("t")
	svc := NewTraverseService(g, nil)

	res, err := svc.Traverse(TraverseRequest{Start: "a", LinkTypes: outOnly("t"), MaxDepth: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Paths[0].Reason; got != TerminatedCycle {
		t.Fatalf("with MaxDepth=1 a first-hop self-loop must still report cycle, got %s", got)
	}
}

// Parallel links from the same source: one closes a cycle, the other does not.
// They are independent paths; cycle detection on one must not kill the other.
func TestParallelLinksMixedCycleAndClean(t *testing.T) {
	g := buildGraph(t,
		[]ObjectID{"a", "b"},
		[]Link{
			{ID: "e1", Type: "t", Source: "a", Target: "b"},
			{ID: "e2", Type: "t", Source: "b", Target: "a"}, // closes a->b->a
			{ID: "e3", Type: "t", Source: "b", Target: "b"}, // self loop on b
			{ID: "e4", Type: "t", Source: "b", Target: "a"}, // parallel back-edge to a
		},
	)
	g.DefineLinkType("t")
	svc := NewTraverseService(g, nil)

	res, err := svc.Traverse(TraverseRequest{Start: "a", LinkTypes: outOnly("t"), MaxDepth: 10})
	if err != nil {
		t.Fatal(err)
	}

	got := reasons(res)
	want := map[string]TerminalReason{
		"[a b a]": TerminatedCycle,
		"[a b b]": TerminatedCycle,
	}
	if len(got) != len(want) {
		t.Fatalf("want %d paths, got %d: %+v", len(want), len(got), got)
	}
	for nodes, reason := range want {
		if got[nodes] != reason {
			t.Fatalf("path %s: want %s, got %s", nodes, reason, got[nodes])
		}
	}
}

// Diamond: the same object reached by two disjoint paths must NOT look like
// a cycle; both branches keep expanding to the natural boundary.
func TestDiamondMergeIsNotCycle(t *testing.T) {
	g := buildGraph(t,
		[]ObjectID{"s", "b", "c", "d"},
		[]Link{
			{ID: "e1", Type: "t", Source: "s", Target: "b"},
			{ID: "e2", Type: "t", Source: "s", Target: "c"},
			{ID: "e3", Type: "t", Source: "b", Target: "d"},
			{ID: "e4", Type: "t", Source: "c", Target: "d"},
		},
	)
	g.DefineLinkType("t")
	svc := NewTraverseService(g, nil)

	res, err := svc.Traverse(TraverseRequest{Start: "s", LinkTypes: outOnly("t"), MaxDepth: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range res.Paths {
		if p.Reason == TerminatedCycle {
			t.Fatalf("diamond must contain no cycle, got cycle path: %v", p.Nodes)
		}
		if p.Reason != TerminatedBoundary {
			t.Fatalf("diamond paths end at boundary, got %s on %v", p.Reason, p.Nodes)
		}
	}
	if len(res.Paths) != 2 {
		t.Fatalf("diamond yields 2 boundary paths, got %d", len(res.Paths))
	}
}

// One traversal may carry cycle paths and depth-truncated paths together;
// the two classifications never collapse into one.
func TestCycleAndDepthTruncationCoexist(t *testing.T) {
	g := buildGraph(t,
		[]ObjectID{"a", "b", "c", "d", "e"},
		[]Link{
			{ID: "e1", Type: "t", Source: "a", Target: "b"},
			{ID: "e2", Type: "t", Source: "b", Target: "a"}, // cycle branch
			{ID: "e3", Type: "t", Source: "a", Target: "c"},
			{ID: "e4", Type: "t", Source: "c", Target: "d"}, // long branch truncated at depth 2
			{ID: "e5", Type: "t", Source: "d", Target: "e"},
		},
	)
	g.DefineLinkType("t")
	svc := NewTraverseService(g, nil)

	res, err := svc.Traverse(TraverseRequest{Start: "a", LinkTypes: outOnly("t"), MaxDepth: 2})
	if err != nil {
		t.Fatal(err)
	}

	sawCycle, sawDepth := false, false
	for _, p := range res.Paths {
		switch fmt.Sprint(p.Nodes) {
		case "[a b a]":
			if p.Reason != TerminatedCycle {
				t.Fatalf("[a b a] must be cycle, got %s", p.Reason)
			}
			sawCycle = true
		case "[a c d]":
			if p.Reason != TerminatedDepthLimit {
				t.Fatalf("[a c d e] must be depth-limit, got %s", p.Reason)
			}
			sawDepth = true
		}
	}
	if !sawCycle || !sawDepth {
		t.Fatalf("need both cycle and depth-limit paths, got %+v", res.Paths)
	}
}

// A genuine longer cycle is detected at the hop that closes it.
func TestLongerCycleDetected(t *testing.T) {
	g := buildGraph(t,
		[]ObjectID{"a", "b", "c"},
		[]Link{
			{ID: "e1", Type: "t", Source: "a", Target: "b"},
			{ID: "e2", Type: "t", Source: "b", Target: "c"},
			{ID: "e3", Type: "t", Source: "c", Target: "b"}, // b repeats within path
		},
	)
	g.DefineLinkType("t")
	svc := NewTraverseService(g, nil)

	res, err := svc.Traverse(TraverseRequest{Start: "a", LinkTypes: outOnly("t"), MaxDepth: 10})
	if err != nil {
		t.Fatal(err)
	}
	p := res.Paths[0]
	if p.Reason != TerminatedCycle || fmt.Sprint(p.Nodes) != "[a b c b]" {
		t.Fatalf("want cycle [a b c b], got %s %v", p.Reason, p.Nodes)
	}
}

// Boundary termination: leaf with no outgoing links.
func TestBoundaryTermination(t *testing.T) {
	g := buildGraph(t,
		[]ObjectID{"a", "b"},
		[]Link{{ID: "e1", Type: "t", Source: "a", Target: "b"}},
	)
	g.DefineLinkType("t")
	svc := NewTraverseService(g, nil)

	res, err := svc.Traverse(TraverseRequest{Start: "a", LinkTypes: outOnly("t"), MaxDepth: 10})
	if err != nil {
		t.Fatal(err)
	}
	if res.Paths[0].Reason != TerminatedBoundary || fmt.Sprint(res.Paths[0].Nodes) != "[a b]" {
		t.Fatalf("want boundary [a b], got %+v", res.Paths)
	}
}

// Validation errors are mutually exclusive and reported in a fixed order.
func TestValidationOrder(t *testing.T) {
	g := NewMemGraph()
	g.DefineLinkType("t")
	svc := NewTraverseService(g, nil)

	// 1. start missing dominates everything else wrong at once.
	_, err := svc.Traverse(TraverseRequest{Start: "ghost", LinkTypes: nil, MaxDepth: 0})
	if err != ErrStartObjectNotFound {
		t.Fatalf("want ErrStartObjectNotFound, got %v", err)
	}

	mustAdd(t, g, "a")
	// 2. empty direction set beats bad depth.
	_, err = svc.Traverse(TraverseRequest{Start: "a", LinkTypes: map[LinkType]Direction{}, MaxDepth: 0})
	if err != ErrEmptyOrUndefinedLinkTypes {
		t.Fatalf("want ErrEmptyOrUndefinedLinkTypes, got %v", err)
	}
	// 2b. undefined link type beats bad depth.
	_, err = svc.Traverse(TraverseRequest{Start: "a", LinkTypes: outOnly("nope"), MaxDepth: 0})
	if err != ErrEmptyOrUndefinedLinkTypes {
		t.Fatalf("want ErrEmptyOrUndefinedLinkTypes, got %v", err)
	}
	// 2c. invalid direction value beats bad depth.
	_, err = svc.Traverse(TraverseRequest{Start: "a", LinkTypes: map[LinkType]Direction{"t": Direction(9)}, MaxDepth: 0})
	if err != ErrEmptyOrUndefinedLinkTypes {
		t.Fatalf("want ErrEmptyOrUndefinedLinkTypes, got %v", err)
	}
	// 3. depth last.
	_, err = svc.Traverse(TraverseRequest{Start: "a", LinkTypes: outOnly("t"), MaxDepth: 0})
	if err != ErrInvalidMaxDepth {
		t.Fatalf("want ErrInvalidMaxDepth, got %v", err)
	}
	_, err = svc.Traverse(TraverseRequest{Start: "a", LinkTypes: outOnly("t"), MaxDepth: -3})
	if err != ErrInvalidMaxDepth {
		t.Fatalf("want ErrInvalidMaxDepth, got %v", err)
	}
}

func mustAdd(t *testing.T, g *MemGraph, id ObjectID) {
	if t != nil {
		t.Helper()
	}
	if err := g.AddObject(id); err != nil {
		fatal(t, "%v", err)
	}
}

// Every traversal logs input, per-path terminal classification and the
// ancestor sequence used for the decision.
func TestLoggingRecordsInputPathTerminalAndAncestors(t *testing.T) {
	g := buildGraph(t,
		[]ObjectID{"a", "b"},
		[]Link{
			{ID: "e1", Type: "t", Source: "a", Target: "b"},
			{ID: "e2", Type: "t", Source: "b", Target: "a"},
		},
	)
	g.DefineLinkType("t")
	logger := NewMemoryLogger()
	svc := NewTraverseService(g, logger)

	if _, err := svc.Traverse(TraverseRequest{Start: "a", LinkTypes: outOnly("t"), MaxDepth: 5}); err != nil {
		t.Fatal(err)
	}
	recs := logger.Records()
	if len(recs) != 1 {
		t.Fatalf("want 1 record, got %d", len(recs))
	}
	rec := recs[0]
	if rec.Request.Start != "a" || rec.Request.MaxDepth != 5 {
		t.Fatalf("log must carry input, got %+v", rec.Request)
	}
	p := rec.Result.Paths[0]
	if p.Reason != TerminatedCycle {
		t.Fatalf("logged path must be cycle, got %s", p.Reason)
	}
	if fmt.Sprint(p.Ancestors) != "[a b]" {
		t.Fatalf("decision ancestors must be the current path [a b], got %v", p.Ancestors)
	}
	if rec.Result.AncestorChecks < 1 {
		t.Fatalf("ancestor probe count must be recorded, got %d", rec.Result.AncestorChecks)
	}
	if rendered := logger.Render(); rendered == "" ||
		!strings.Contains(rendered, "terminal=cycle") || !strings.Contains(rendered, "ancestors=[a -> b]") {
		t.Fatalf("rendered log missing required fields:\n%s", rendered)
	}
}
