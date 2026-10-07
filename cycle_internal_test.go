package ontology

import (
	"fmt"
	"reflect"
	"testing"
)

func mkGraph(t *testing.T) *Graph {
	t.Helper()
	g := NewGraph()
	must(t, g.AddObjectType("T", "T"))
	must(t, g.AddLinkType(LinkType{
		ID:            "D",
		Direction:     Directed,
		AllowSelfLoop: true,
		AllowParallel: true,
	}))
	must(t, g.AddLinkType(LinkType{
		ID:            "B",
		Direction:     Bidirectional,
		AllowSelfLoop: true,
		AllowParallel: true,
	}))
	return g
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func addObjs(t *testing.T, g *Graph, caller string, ids ...string) {
	t.Helper()
	for _, id := range ids {
		must(t, g.AddObject(caller, id, "T"))
	}
}

// TestSelfLoop verifies the length-one boundary case.
func TestSelfLoop(t *testing.T) {
	g := mkGraph(t)
	addObjs(t, g, "alice", "a")
	must(t, g.AddLink("alice", "l1", "D", "a", "a"))

	res, err := g.HasCycle("alice")
	must(t, err)
	if !res.HasCycle || !reflect.DeepEqual(res.Evidence, []string{"a", "a"}) {
		t.Fatalf("self loop: got %+v", res)
	}
}

// TestRoundTrip verifies the two-object round trip: one bidirectional link
// and a pair of opposite directed links both qualify.
func TestRoundTrip(t *testing.T) {
	t.Run("bidirectional single link", func(t *testing.T) {
		g := mkGraph(t)
		addObjs(t, g, "alice", "a", "b")
		must(t, g.AddLink("alice", "l", "B", "a", "b"))
		res, err := g.HasCycle("alice")
		must(t, err)
		if !res.HasCycle || !reflect.DeepEqual(res.Evidence, []string{"a", "b", "a"}) {
			t.Fatalf("bidirectional round trip: got %+v", res)
		}
	})
	t.Run("opposite directed links", func(t *testing.T) {
		g := mkGraph(t)
		addObjs(t, g, "alice", "a", "b")
		must(t, g.AddLink("alice", "l1", "D", "a", "b"))
		must(t, g.AddLink("alice", "l2", "D", "b", "a"))
		res, err := g.HasCycle("alice")
		must(t, err)
		if !res.HasCycle || !reflect.DeepEqual(res.Evidence, []string{"a", "b", "a"}) {
			t.Fatalf("opposite links round trip: got %+v", res)
		}
	})
	t.Run("single directed link is acyclic", func(t *testing.T) {
		g := mkGraph(t)
		addObjs(t, g, "alice", "a", "b")
		must(t, g.AddLink("alice", "l1", "D", "a", "b"))
		res, err := g.HasCycle("alice")
		must(t, err)
		if res.HasCycle {
			t.Fatalf("single directed link must not be a cycle: %+v", res)
		}
	})
}

// TestParallelLinksDedupEvidence checks that multiple links of the same type
// between one object pair are traversable but never duplicate objects in the
// returned evidence.
func TestParallelLinksDedupEvidence(t *testing.T) {
	g := mkGraph(t)
	addObjs(t, g, "alice", "a", "b")
	must(t, g.AddLink("alice", "p1", "D", "a", "b"))
	must(t, g.AddLink("alice", "p2", "D", "a", "b"))
	must(t, g.AddLink("alice", "q1", "D", "b", "a"))
	res, err := g.HasCycle("alice")
	must(t, err)
	if !res.HasCycle {
		t.Fatal("expected cycle through parallel links")
	}
	want := []string{"a", "b", "a"}
	if !reflect.DeepEqual(res.Evidence, want) {
		t.Fatalf("parallel links evidence: got %v want %v", res.Evidence, want)
	}
}

// TestUniqueCycleLinkExcluded covers the mandatory ordering: existence filter
// first, traversal filter second, cycle detection last.
func TestUniqueCycleLinkExcluded(t *testing.T) {
	g := mkGraph(t)
	addObjs(t, g, "admin", "a", "b", "c")
	must(t, g.AddLink("admin", "ab", "D", "a", "b"))
	must(t, g.AddLink("admin", "bc", "D", "b", "c"))
	must(t, g.AddLink("admin", "ca", "D", "c", "a"))

	// bob sees all objects but cannot traverse the unique closing edge.
	must(t, g.GrantExistence("bob", "a", "b", "c"))
	must(t, g.GrantTraversal("bob", "ab", "bc"))
	res, err := g.HasCycle("bob")
	must(t, err)
	if res.HasCycle {
		t.Fatalf("cycle must not exist while closing edge excluded: %+v", res)
	}

	// Restoring traversal on the next independent call flips the result.
	must(t, g.GrantTraversal("bob", "ca"))
	res, err = g.HasCycle("bob")
	must(t, err)
	if !res.HasCycle || !reflect.DeepEqual(res.Evidence, []string{"a", "b", "c", "a"}) {
		t.Fatalf("after traversal restore: got %+v", res)
	}

	// Revoking again must not reuse any earlier conclusion.
	must(t, g.RevokeTraversal("bob", "ca"))
	res, err = g.HasCycle("bob")
	must(t, err)
	if res.HasCycle {
		t.Fatalf("stale conclusion reused after revoke: %+v", res)
	}
}

// TestExistenceBeforeTraversal pins the filter order: an invisible endpoint
// removes a link even when traversal is granted; making the object visible
// afterwards activates the link without regranting traversal.
func TestExistenceBeforeTraversal(t *testing.T) {
	g := mkGraph(t)
	addObjs(t, g, "admin", "a", "b")
	must(t, g.AddLink("admin", "ab", "D", "a", "b"))
	must(t, g.AddLink("admin", "ba", "D", "b", "a"))
	must(t, g.GrantTraversal("carol", "ab", "ba"))

	// No existence permission at all: empty visible set, no cycle, no error.
	res, err := g.HasCycle("carol")
	must(t, err)
	if res.HasCycle {
		t.Fatalf("invisible everything: %+v", res)
	}

	must(t, g.GrantExistence("carol", "a"))
	res, _ = g.HasCycle("carol")
	if res.HasCycle {
		t.Fatalf("one invisible endpoint must suppress both links: %+v", res)
	}

	must(t, g.GrantExistence("carol", "b"))
	res, _ = g.HasCycle("carol")
	if !res.HasCycle {
		t.Fatalf("links should activate once both endpoints exist: %+v", res)
	}
}

// TestInvalidCallerAndEmpty verifies the special-result precedence.
func TestInvalidCallerAndEmpty(t *testing.T) {
	g := mkGraph(t)
	addObjs(t, g, "admin", "a")
	must(t, g.AddLink("admin", "s", "D", "a", "a"))

	if _, err := g.HasCycle(""); err != ErrInvalidCaller {
		t.Fatalf("empty caller: got %v want ErrInvalidCaller", err)
	}
	if _, err := g.HasCycle("  x "); err != ErrInvalidCaller {
		t.Fatalf("whitespace caller: got %v want ErrInvalidCaller", err)
	}

	// Unknown caller: empty visible set is a normal acyclic answer.
	res, err := g.HasCycle("ghost")
	must(t, err)
	if res.HasCycle {
		t.Fatal("unknown caller must be acyclic")
	}
}

// TestStartObjectIndependence stresses that repeated calls on an unchanged
// graph return byte-identical results, irrespective of interleaved readonly
// queries or call history.
func TestStartObjectIndependence(t *testing.T) {
	g := mkGraph(t)
	addObjs(t, g, "admin", "n1", "n2", "n3", "n4")
	must(t, g.AddLink("admin", "e1", "D", "n1", "n2"))
	must(t, g.AddLink("admin", "e2", "D", "n2", "n3"))
	must(t, g.AddLink("admin", "e3", "D", "n3", "n1"))
	must(t, g.AddLink("admin", "e4", "D", "n4", "n1"))
	must(t, g.AddLink("admin", "e5", "D", "n2", "n4"))
	must(t, g.AddLink("admin", "e6", "D", "n4", "n3"))

	var first CycleResult
	for i := 0; i < 10; i++ {
		// Readonly-only operations between detections.
		_, _ = g.HasCycle("nobody")
		res, err := g.HasCycle("admin")
		must(t, err)
		if i == 0 {
			first = res
			if !res.HasCycle || len(res.Evidence) != 4 {
				t.Fatalf("bad first result: %+v", res)
			}
		} else if !reflect.DeepEqual(res.Evidence, first.Evidence) ||
			res.HasCycle != first.HasCycle {
			t.Fatalf("non-deterministic result on call %d: %v vs %v",
				i, res.Evidence, first.Evidence)
		}
	}

	// A second caller with identical permissions but different creation
	// history must obtain the identical evidence.
	must(t, g.GrantExistence("dave", "n1", "n2", "n3", "n4"))
	must(t, g.GrantTraversal("dave", "e1", "e2", "e3", "e4", "e5", "e6"))
	res, _ := g.HasCycle("dave")
	if !reflect.DeepEqual(res.Evidence, first.Evidence) {
		t.Fatalf("history-dependent evidence: %v vs %v", res.Evidence, first.Evidence)
	}
}

// TestInvisibleGrowthIgnored proves the cost metric stays tied to the visible
// subgraph when large amounts of invisible data are appended.
func TestInvisibleGrowthIgnored(t *testing.T) {
	g := mkGraph(t)
	addObjs(t, g, "admin", "a", "b", "c")
	must(t, g.AddLink("admin", "ab", "D", "a", "b"))
	must(t, g.AddLink("admin", "bc", "D", "b", "c"))
	must(t, g.AddLink("admin", "ca", "D", "c", "a"))

	must(t, g.GrantExistence("zoe", "a", "b", "c"))
	must(t, g.GrantTraversal("zoe", "ab", "bc", "ca"))

	_, base, err := g.hasCycleWithStats("zoe")
	must(t, err)

	// Append a large invisible island plus links invisible to zoe.
	for i := 0; i < 500; i++ {
		x := fmt.Sprintf("z%04d", i)
		y := fmt.Sprintf("w%04d", i)
		must(t, g.AddObject("admin", x, "T"))
		must(t, g.AddObject("admin", y, "T"))
		must(t, g.AddLink("admin", "il"+x, "D", x, y))
	}

	_, grown, err := g.hasCycleWithStats("zoe")
	must(t, err)
	if grown.VisibleObjects != base.VisibleObjects ||
		grown.ActiveLinks != base.ActiveLinks ||
		grown.NodesExamined != base.NodesExamined ||
		grown.LinksExamined != base.LinksExamined {
		t.Fatalf("metric grew with invisible data: base=%+v grown=%+v",
			base, grown)
	}
}

// TestAuditLog checks every call records input, output and evidence.
func TestAuditLog(t *testing.T) {
	g := mkGraph(t)
	addObjs(t, g, "admin", "a")
	must(t, g.AddLink("admin", "s", "D", "a", "a"))
	_, _ = g.HasCycle("admin")
	_, _ = g.HasCycle("ghost")
	log := g.AuditLog()
	if len(log) != 2 {
		t.Fatalf("audit len: %d", len(log))
	}
	if log[0].Caller != "admin" || !log[0].HasCycle ||
		!reflect.DeepEqual(log[0].Evidence, []string{"a", "a"}) {
		t.Fatalf("audit[0]: %+v", log[0])
	}
	if log[1].Caller != "ghost" || log[1].HasCycle {
		t.Fatalf("audit[1]: %+v", log[1])
	}
}
