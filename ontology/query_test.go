package ontology

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

func buildLineGraph(t *testing.T) (*Graph, *PermissionState) {
	t.Helper()
	g := NewGraph()
	types := []ObjectType{"A", "B", "C", "D"}
	for i, ty := range types {
		must(t, g.AddObject(ObjectID(fmt.Sprintf("o%d", i)), ty))
	}
	for i := 0; i < 3; i++ {
		must(t, g.AddLink(Link{
			Type: "next",
			From: ObjectID(fmt.Sprintf("o%d", i)),
			To:   ObjectID(fmt.Sprintf("o%d", i+1)),
			Cost: 1,
		}))
	}
	ps := NewPermissionState()
	must(t, ps.UpsertGroup("all", 1))
	must(t, ps.AddMember("u", "all"))
	return g, ps
}

func TestShortestPathBasicAndCost(t *testing.T) {
	g, ps := buildLineGraph(t)
	must(t, ps.SetObjectDecl("all", "A", Allow))
	must(t, ps.SetObjectDecl("all", "B", Allow))
	must(t, ps.SetObjectDecl("all", "C", Allow))
	must(t, ps.SetObjectDecl("all", "D", Allow))

	eng := NewQueryEngine(ps, g)
	res := eng.ShortestPath(Query{Subject: "u", From: "o0", To: "o3"})
	if res.Status != StatusReachable {
		t.Fatalf("status=%s steps=%v", res.Status, res.Steps)
	}
	if res.Path.Cost != 3 || len(res.Path.ObjectIDs) != 4 {
		t.Fatalf("unexpected path %+v", res.Path)
	}
}

func TestShortestPathCheapestWins(t *testing.T) {
	g, ps := buildLineGraph(t)
	// Cheaper long edge o0 -> o3 of a distinct type.
	must(t, g.AddLink(Link{Type: "jump", From: "o0", To: "o3", Cost: 0.5}))
	must(t, ps.SetLinkDecl("all", "next", Allow))
	must(t, ps.SetLinkDecl("all", "jump", Allow))

	eng := NewQueryEngine(ps, g)
	res := eng.ShortestPath(Query{Subject: "u", From: "o0", To: "o3"})
	if res.Status != StatusReachable || res.Path.Cost != 0.5 {
		t.Fatalf("got status=%s cost=%v", res.Status, pathCost(res))
	}
}

func pathCost(r QueryResult) float64 {
	if r.Path == nil {
		return -1
	}
	return r.Path.Cost
}

func TestLexicographicTieBreak(t *testing.T) {
	g := NewGraph()
	ids := []ObjectID{"s", "a", "b", "t"}
	for _, id := range ids {
		must(t, g.AddObject(id, "N"))
	}
	must(t, g.AddLink(Link{Type: "e", From: "s", To: "b", Cost: 1}))
	must(t, g.AddLink(Link{Type: "e", From: "s", To: "a", Cost: 1}))
	must(t, g.AddLink(Link{Type: "e", From: "a", To: "t", Cost: 1}))
	must(t, g.AddLink(Link{Type: "e", From: "b", To: "t", Cost: 1}))

	ps := NewPermissionState()
	must(t, ps.UpsertGroup("g", 1))
	must(t, ps.AddMember("u", "g"))
	must(t, ps.SetLinkDecl("g", "e", Allow))

	eng := NewQueryEngine(ps, g)
	res := eng.ShortestPath(Query{Subject: "u", From: "s", To: "t"})
	if res.Status != StatusReachable {
		t.Fatalf("status=%s", res.Status)
	}
	want := []ObjectID{"s", "a", "t"}
	if len(res.Path.ObjectIDs) != len(want) {
		t.Fatalf("want %v got %v", want, res.Path.ObjectIDs)
	}
	for i := range want {
		if res.Path.ObjectIDs[i] != want[i] {
			t.Fatalf("want %v got %v", want, res.Path.ObjectIDs)
		}
	}
}

func TestUnreachableVsAmbiguous(t *testing.T) {
	g, ps := buildLineGraph(t)
	// Default deny everywhere: definite unreachable.
	eng := NewQueryEngine(ps, g)
	res := eng.ShortestPath(Query{Subject: "u", From: "o0", To: "o3"})
	if res.Status != StatusUnreachable {
		t.Fatalf("want unreachable, got %s", res.Status)
	}

	// Allow first edge; leave the rest default deny: still unreachable.
	must(t, ps.SetObjectDecl("all", "A", Allow))
	must(t, ps.SetObjectDecl("all", "B", Allow))
	res = eng.ShortestPath(Query{Subject: "u", From: "o0", To: "o3"})
	if res.Status != StatusUnreachable {
		t.Fatalf("partial allow must stay unreachable, got %s", res.Status)
	}

	// Add a second equal-priority group that contradicts on object type D,
	// producing an ambiguous edge that, if allowed, completes the path.
	must(t, ps.UpsertGroup("other", 1))
	must(t, ps.AddMember("u", "other"))
	must(t, ps.SetObjectDecl("other", "D", Deny))
	must(t, ps.SetObjectDecl("all", "C", Allow))
	must(t, ps.SetObjectDecl("all", "D", Allow))
	// Equal-priority link Allow on the last edge clashes with other's Deny.
	must(t, ps.SetLinkDecl("other", "next", Allow))
	res = eng.ShortestPath(Query{Subject: "u", From: "o0", To: "o3"})
	if res.Status != StatusAmbiguous {
		t.Fatalf("want ambiguous, got %s", res.Status)
	}
}

func TestAmbiguousDeadEndDoesNotPoisonUnreachable(t *testing.T) {
	g, ps := buildLineGraph(t)
	// A branch from o0 leads through an ambiguous edge into a dead end that
	// cannot reach the target; the target itself stays definite unreachable.
	must(t, g.AddObject("z0", "Z"))
	must(t, g.AddObject("z1", "Z"))
	must(t, g.AddLink(Link{Type: "dead", From: "o0", To: "z0", Cost: 1}))
	must(t, g.AddLink(Link{Type: "dead", From: "z0", To: "z1", Cost: 1}))
	must(t, ps.SetLinkDecl("all", "dead", Allow))
	must(t, ps.UpsertGroup("gB", 1))
	must(t, ps.AddMember("u", "gB"))
	must(t, ps.SetObjectDecl("gB", "Z", Deny))

	eng := NewQueryEngine(ps, g)
	res := eng.ShortestPath(Query{Subject: "u", From: "o0", To: "o3"})
	// The only ambiguous edge enters a node that cannot reach t either way.
	if res.Status != StatusUnreachable {
		t.Fatalf("dead-end ambiguity must not flip to ambiguous, got %s", res.Status)
	}
}

func TestSnapshotBoundaryDuringMutation(t *testing.T) {
	g, ps := buildLineGraph(t)
	eng := NewQueryEngine(ps, g)

	must(t, ps.SetLinkDecl("all", "next", Allow))
	pinned := eng.Pin()
	before := pinned.Run(Query{Subject: "u", From: "o0", To: "o3"})
	if before.Status != StatusReachable {
		t.Fatalf("pre-mutation query: %s", before.Status)
	}

	// Mutate after the pin: revoke every traversal.
	must(t, ps.SetLinkDecl("all", "next", Deny))
	afterPin := pinned.Run(Query{Subject: "u", From: "o0", To: "o3"})
	if afterPin.Status != StatusReachable || afterPin.StateVersion != before.StateVersion {
		t.Fatalf("pinned query changed: %s v%d vs v%d", afterPin.Status, afterPin.StateVersion, before.StateVersion)
	}

	fresh := eng.ShortestPath(Query{Subject: "u", From: "o0", To: "o3"})
	if fresh.Status != StatusUnreachable || fresh.StateVersion != afterPin.StateVersion+1 {
		t.Fatalf("fresh query should see the deny: status=%s v%d", fresh.Status, fresh.StateVersion)
	}
}

func TestCountersAreQueryLocal(t *testing.T) {
	g, ps := buildLineGraph(t)
	// Add unrelated objects/links that the query never visits.
	must(t, g.AddObject("x0", "X"))
	must(t, g.AddObject("x1", "X"))
	for i := 0; i < 20; i++ {
		must(t, g.AddLink(Link{
			Type: LinkType(fmt.Sprintf("x%d", i)),
			From: "x0", To: "x1", Cost: 1,
		}))
	}
	must(t, ps.SetLinkDecl("all", "next", Allow))

	eng := NewQueryEngine(ps, g)
	res := eng.ShortestPath(Query{Subject: "u", From: "o0", To: "o3"})
	if res.Status != StatusReachable {
		t.Fatalf("status=%s", res.Status)
	}
	// The chain uses three distinct (next, source, target) type signatures.
	if res.Counters.LinkEvaluations != 3 {
		t.Fatalf("evaluations=%d want 3", res.Counters.LinkEvaluations)
	}
	if res.Counters.Relaxations != 3 {
		t.Fatalf("relaxations=%d want 3", res.Counters.Relaxations)
	}
}

func TestConcurrentQueriesAndMutationsAreConsistent(t *testing.T) {
	g, ps := buildLineGraph(t)
	must(t, ps.SetLinkDecl("all", "next", Allow))
	eng := NewQueryEngine(ps, g)

	var wg sync.WaitGroup
	errCh := make(chan error, 200)
	var truthMu sync.Mutex
	truth := map[int64]bool{}
	record := func() {
		s := ps.Snapshot()
		truthMu.Lock()
		truth[s.version] = s.linkDecls["all"]["next"] == Allow
		truthMu.Unlock()
	}
	record()

	// Mutator flips the declaration on committed versions.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			d := Allow
			if i%2 == 1 {
				d = Deny
			}
			_ = ps.SetLinkDecl("all", "next", d)
			record()
		}
	}()

	// Readers: every result must be a self-consistent committed version.
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := eng.ShortestPath(Query{Subject: "u", From: "o0", To: "o3"})
			truthMu.Lock()
			allow, known := truth[r.StateVersion]
			truthMu.Unlock()
			steps := r.Steps
			if len(steps) == 0 {
				errCh <- fmt.Errorf("no steps recorded for v%d", r.StateVersion)
				return
			}
			if !known {
				return // version slipped past our recording window
			}
			for _, s := range steps {
				// The only declared link type in the line graph is "next";
				// its verdict at version v must equal expectedAllowAtVersion.
				verd := s.Verdict.Verdict == VerdictAllow
				if s.LinkType == "next" && verd != allow {
					errCh <- fmt.Errorf("mixed versions in one query: v%d step=%s->%s verdict=%s",
						r.StateVersion, s.From, s.To, s.Verdict.Verdict)
					return
				}
			}
			if allow && r.Status != StatusReachable {
				errCh <- fmt.Errorf("allow version v%d produced %s", r.StateVersion, r.Status)
			}
			if !allow && r.Status != StatusUnreachable {
				errCh <- fmt.Errorf("deny version v%d produced %s", r.StateVersion, r.Status)
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
}

func TestTraceRecordsEveryExaminedStep(t *testing.T) {
	g, ps := buildLineGraph(t)
	must(t, ps.SetLinkDecl("all", "next", Deny))
	eng := NewQueryEngine(ps, g)
	res := eng.ShortestPath(Query{Subject: "u", From: "o0", To: "o3"})
	if len(res.Steps) != 1 {
		t.Fatalf("want exactly 1 examined step, got %d", len(res.Steps))
	}
	s := res.Steps[0]
	if s.From != "o0" || s.To != "o1" || !strings.Contains(s.Verdict.Reason, "link-layer-deny") {
		t.Fatalf("unexpected step %+v", s)
	}
}
