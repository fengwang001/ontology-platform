package ontology

import (
	"errors"
	"fmt"
	"testing"
)

const callerID = "alice"

func mustGraph(t *testing.T) *Graph {
	t.Helper()
	g := NewGraph()
	must(t, g.AddObjectType(ObjectType{ID: "T"}))
	must(t, g.AddLinkType(LinkType{ID: "L", FromType: "T", ToType: "T", Direction: Directed}))
	must(t, g.AddLinkType(LinkType{ID: "B", FromType: "T", ToType: "T", Direction: Bidirectional}))
	for _, id := range []ID{"s", "x", "y", "t", "u"} {
		must(t, g.AddObject(ObjectInstance{ID: id, ObjectType: "T"}))
	}
	return g
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func grantVis(g *Graph, ids ...ID) {
	for _, id := range ids {
		if err := g.GrantExistence(callerID, id); err != nil {
			panic(err)
		}
	}
}

// TestDecisionOrder verifies invalid id > absent > invisible > search.
func TestDecisionOrder(t *testing.T) {
	g := mustGraph(t)

	if _, _, err := g.Reachable("bad id!", "t", callerID); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("invalid from: got %v", err)
	}
	if _, _, err := g.Reachable("s", "bad id!", callerID); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("invalid to: got %v", err)
	}
	if _, _, err := g.Reachable("ghost", "t", callerID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("absent from: got %v", err)
	}
	if _, _, err := g.Reachable("s", "ghost", callerID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("absent to: got %v", err)
	}

	// Both exist but invisible -> Restricted even though no traversal rights.
	out, trace, err := g.Reachable("s", "t", callerID)
	if err != nil || out != Restricted || trace.Reason != "endpoint_invisible" {
		t.Fatalf("invisible endpoints: out=%v reason=%q err=%v", out, trace.Reason, err)
	}
}

// TestExistenceVsTraversalDifference is the headline permission distinction.
func TestExistenceVsTraversalDifference(t *testing.T) {
	g := mustGraph(t)
	must(t, g.AddLink(LinkInstance{ID: "e1", LinkType: "L", Tail: "s", Head: "t"}))
	must(t, g.GrantExistence(callerID, "s"))
	must(t, g.GrantExistence(callerID, "t"))

	// Endpoints visible, traversal missing: unique path is truncated.
	out, trace, err := g.Reachable("s", "t", callerID)
	if err != nil {
		t.Fatal(err)
	}
	if out != Restricted || trace.Reason != "denied_arc_may_reach_target" {
		t.Fatalf("want restricted by traversal, got %v (%s)", out, trace.Reason)
	}
	if len(trace.DeniedCandidates) != 1 {
		t.Fatalf("want 1 denied candidate, got %d", len(trace.DeniedCandidates))
	}

	// Grant traversal: now reachable.
	must(t, g.GrantTraversal(callerID, "L"))
	out, trace, err = g.Reachable("s", "t", callerID)
	if err != nil || out != Reachable {
		t.Fatalf("want reachable, got %v (%s) err=%v", out, trace.Reason, err)
	}

	// Revoke target visibility: Restricted again, but for existence reasons,
	// and the trace reason must not leak traversal topology.
	must(t, g.RevokeExistence(callerID, "t"))
	out, trace, err = g.Reachable("s", "t", callerID)
	if err != nil || out != Restricted || trace.Reason != "endpoint_invisible" {
		t.Fatalf("want restricted by existence, got %v (%s) err=%v", out, trace.Reason, err)
	}
}

// TestRestrictedVsUnreachableBoundary is the unique-possible-path case and
// its opposite: the blocked branch structurally cannot reach the target.
func TestRestrictedVsUnreachableBoundary(t *testing.T) {
	// Case A: s --L(denied)--> t is the ONLY path.
	g := mustGraph(t)
	must(t, g.AddLink(LinkInstance{ID: "e1", LinkType: "L", Tail: "s", Head: "t"}))
	must(t, g.GrantExistence(callerID, "s"))
	must(t, g.GrantExistence(callerID, "t"))
	out, _, _ := g.Reachable("s", "t", callerID)
	if out != Restricted {
		t.Fatalf("unique truncated path must be Restricted, got %v", out)
	}

	// Case B: denied arc leads into a sink component that cannot reach t,
	// while the traversable component also cannot reach t.
	g2 := NewGraph()
	must(t, g2.AddObjectType(ObjectType{ID: "T"}))
	must(t, g2.AddLinkType(LinkType{ID: "L", FromType: "T", ToType: "T", Direction: Directed}))
	for _, id := range []ID{"s", "t", "z"} {
		must(t, g2.AddObject(ObjectInstance{ID: id, ObjectType: "T"}))
	}
	must(t, g2.AddLink(LinkInstance{ID: "e", LinkType: "L", Tail: "s", Head: "z"}))
	must(t, g2.GrantExistence(callerID, "s"))
	must(t, g2.GrantExistence(callerID, "t"))
	must(t, g2.GrantExistence(callerID, "z"))
	out2, trace2, _ := g2.Reachable("s", "t", callerID)
	if out2 != Unreachable || trace2.Reason != "denied_arcs_cannot_reach_target" {
		t.Fatalf("blocked sink branch must be Unreachable, got %v (%s)", out2, trace2.Reason)
	}

	// Case C: no arcs at all: plain exhausted search.
	g3 := mustGraph(t)
	grantVis(g3, "s", "u")
	out3, trace3, _ := g3.Reachable("s", "u", callerID)
	if out3 != Unreachable || trace3.Reason != "exhausted_search_no_candidate" {
		t.Fatalf("disconnected visible nodes must be Unreachable, got %v (%s)", out3, trace3.Reason)
	}
}

// TestDirectionAsymmetry: swapping endpoints on a directed link flips the
// answer; a bidirectional link stays symmetric.
func TestDirectionAsymmetry(t *testing.T) {
	g := mustGraph(t)
	must(t, g.AddLink(LinkInstance{ID: "e", LinkType: "L", Tail: "s", Head: "t"}))
	grantVis(g, "s", "t")
	must(t, g.GrantTraversal(callerID, "L"))

	if out, _, _ := g.Reachable("s", "t", callerID); out != Reachable {
		t.Fatalf("forward directed: want Reachable, got %v", out)
	}
	if out, _, _ := g.Reachable("t", "s", callerID); out != Unreachable {
		t.Fatalf("reverse directed: want Unreachable, got %v", out)
	}

	must(t, g.AddLink(LinkInstance{ID: "b", LinkType: "B", Tail: "x", Head: "y"}))
	grantVis(g, "x", "y")
	must(t, g.GrantTraversal(callerID, "B"))
	if out, _, _ := g.Reachable("x", "y", callerID); out != Reachable {
		t.Fatalf("bidirectional xy: got %v", out)
	}
	if out, _, _ := g.Reachable("y", "x", callerID); out != Reachable {
		t.Fatalf("bidirectional yx: got %v", out)
	}
}

// TestRevocationBoundary fixes the snapshot before the search and revokes
// inside the hook: the in-flight query must still answer Reachable, while
// every query started after the accepted revocation answers Restricted.
func TestRevocationBoundary(t *testing.T) {
	g := mustGraph(t)
	must(t, g.AddLink(LinkInstance{ID: "e", LinkType: "L", Tail: "s", Head: "t"}))
	grantVis(g, "s", "t")
	must(t, g.GrantTraversal(callerID, "L"))

	var inFlight Outcome
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		out, _, err := g.ReachableWithHook("s", "t", callerID, func() {
			close(started)
			<-release
			must(t, g.RevokeTraversal(callerID, "L"))
		})
		if err != nil {
			t.Errorf("in-flight query: %v", err)
		}
		inFlight = out
		close(done)
	}()

	<-started
	close(release)
	<-done
	if inFlight != Reachable {
		t.Fatalf("in-flight query must keep old permission, got %v", inFlight)
	}
	if out, _, _ := g.Reachable("s", "t", callerID); out != Restricted {
		t.Fatalf("post-revocation query must be Restricted, got %v", out)
	}
}

// TestMetricIndependentOfForbiddenRegion appends a huge region attached to
// the reachable core by a single directed arc the caller cannot traverse.
// Traversing it inward can never return to the target, so the internal
// "actually attempted" measure must stay constant as the region grows.
func TestMetricIndependentOfForbiddenRegion(t *testing.T) {
	build := func(n int) (*Graph, Metrics) {
		g := mustGraph(t)
		// Reachable route s -L-> x -L-> t.
		must(t, g.AddLink(LinkInstance{ID: "e1", LinkType: "L", Tail: "s", Head: "x"}))
		must(t, g.AddLink(LinkInstance{ID: "e2", LinkType: "L", Tail: "x", Head: "t"}))
		must(t, g.AddObject(ObjectInstance{ID: "z0", ObjectType: "T"}))
		grantVis(g, "s", "x", "t")
		// s also has a denied L-arc into a large directed chain sink
		// (arcs inside the chain are irrelevant: the entry itself is denied).
		must(t, g.AddLink(LinkInstance{ID: "entry", LinkType: "L", Tail: "s", Head: "z0"}))
		prev := "z0"
		for i := 1; i < n; i++ {
			id := fmt.Sprintf("z%d", i)
			must(t, g.AddObject(ObjectInstance{ID: id, ObjectType: "T"}))
			must(t, g.AddLink(LinkInstance{ID: fmt.Sprintf("ze%d", i), LinkType: "L", Tail: prev, Head: id}))
			prev = id
		}
		must(t, g.GrantTraversal(callerID, "L"))
		_, trace, err := g.Reachable("s", "t", callerID)
		if err != nil {
			t.Fatal(err)
		}
		return g, trace.Metrics
	}

	_, small := build(5)
	_, large := build(5000)
	if small != large {
		t.Fatalf("metrics must be independent of forbidden sink size:\nsmall=%+v\nlarge=%+v", small, large)
	}
	// Forward BFS expands s and x; s scans the permitted route and the denied
	// entry, x scans its single arc. Nothing inside the sink is touched.
	if small.ObjectsExpanded != 2 || small.LinksAttempted != 3 || small.BacktrackObjects != 0 {
		t.Fatalf("unexpected forward measure: %+v", small)
	}
}
