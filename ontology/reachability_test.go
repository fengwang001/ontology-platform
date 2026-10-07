package ontology

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
)

const (
	ot = "Thing"
	lt = "rel"
	bi = "friend"
)

type recTracer struct{ events []map[string]any }

func (r *recTracer) Trace(ev map[string]any) {
	cp := make(map[string]any, len(ev))
	for k, v := range ev {
		cp[k] = v
	}
	r.events = append(r.events, cp)
}

type hookTracer struct{ hook func(map[string]any) }

func (h *hookTracer) Trace(ev map[string]any) { h.hook(ev) }

func mustGraph(t *testing.T) *Graph {
	t.Helper()
	g := NewGraph()
	if err := g.AddObjectType(ot); err != nil {
		t.Fatal(err)
	}
	if err := g.AddLinkType(lt, Unidirectional); err != nil {
		t.Fatal(err)
	}
	if err := g.AddLinkType(bi, Bidirectional); err != nil {
		t.Fatal(err)
	}
	return g
}

func addObjs(t *testing.T, g *Graph, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if err := g.AddObject(Object{ID: id, ObjectType: ot}); err != nil {
			t.Fatal(err)
		}
	}
}

func addLink(t *testing.T, g *Graph, id, kind, src, dst string) {
	t.Helper()
	if err := g.AddLink(Link{ID: id, LinkType: kind, Src: src, Dst: dst}); err != nil {
		t.Fatal(err)
	}
}

func grantAll(t *testing.T, g *Graph, c CallerID, objs []string, linkTypes ...string) {
	t.Helper()
	for _, o := range objs {
		g.GrantExistence(c, o)
	}
	for _, l := range linkTypes {
		g.GrantTraversal(c, l)
	}
}

func classify(t *testing.T, g *Graph, start, end string, c CallerID) (Outcome, Reason) {
	t.Helper()
	out, reason, _, err := g.ReachableFromTraced(context.Background(), start, end, c, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return out, reason
}

func errKind(t *testing.T, g *Graph, start, end string, c CallerID) string {
	t.Helper()
	_, err := g.ReachableFrom(context.Background(), start, end, c)
	var qe *QueryError
	if !errors.As(err, &qe) {
		t.Fatalf("want QueryError, got %v", err)
	}
	return qe.Kind
}

func itoa(i int) string { return strconv.Itoa(i) }

func TestPrecedenceInvalidThenMissingThenInvisible(t *testing.T) {
	g := mustGraph(t)
	addObjs(t, g, "a")
	const c CallerID = "u"

	if got := errKind(t, g, "bad id!", "a", c); got != "invalid_id" {
		t.Fatalf("invalid start: got %q", got)
	}
	if got := errKind(t, g, "a", "bad id!", c); got != "invalid_id" {
		t.Fatalf("invalid end: got %q", got)
	}
	if got := errKind(t, g, "ghost", "a", c); got != "missing_start" {
		t.Fatalf("missing start: got %q", got)
	}
	if got := errKind(t, g, "a", "ghost", c); got != "missing_end" {
		t.Fatalf("missing end: got %q", got)
	}

	if out, _ := classify(t, g, "a", "a", c); out != RestrictedUnknown {
		t.Fatalf("invisible endpoint: got %s", out)
	}
	if got := errKind(t, g, "ghost", "a", c); got != "missing_start" {
		t.Fatalf("missing must beat invisible: got %q", got)
	}
}

func TestExistenceVsTraversalPermissionDifference(t *testing.T) {
	g0 := mustGraph(t)
	addObjs(t, g0, "a")
	const c0 CallerID = "u"
	if out, _ := classify(t, g0, "a", "a", c0); out != RestrictedUnknown {
		t.Fatalf("self-query invisible: got %s", out)
	}
	g0.GrantExistence(c0, "a")
	if out, reason := classify(t, g0, "a", "a", c0); out != Reachable || reason != ReasonCertifiedPath {
		t.Fatalf("zero-length visible self path: %s/%s", out, reason)
	}

	g := mustGraph(t)
	addObjs(t, g, "a", "b")
	addLink(t, g, "l1", lt, "a", "b")
	const c CallerID = "u"

	if out, reason := classify(t, g, "a", "b", c); out != RestrictedUnknown || reason != ReasonInvisibleEndpoint {
		t.Fatalf("all hidden: %s/%s", out, reason)
	}

	grantAll(t, g, c, []string{"a", "b"})
	if out, reason := classify(t, g, "a", "b", c); out != RestrictedUnknown || reason != ReasonCandidatesTruncated {
		t.Fatalf("traversal denied: %s/%s", out, reason)
	}

	g.GrantTraversal(c, lt)
	if out, reason := classify(t, g, "a", "b", c); out != Reachable || reason != ReasonCertifiedPath {
		t.Fatalf("fully permitted: %s/%s", out, reason)
	}

	g.RevokeExistence(c, "b")
	if out, reason := classify(t, g, "a", "b", c); out != RestrictedUnknown || reason != ReasonInvisibleEndpoint {
		t.Fatalf("end invisible: %s/%s", out, reason)
	}
}

func TestTruncatedVsExhaustedBoundary(t *testing.T) {
	// Two-type chain s -a-> m -b-> t plus an isolated component z -b-> t.
	// Caller holds a only: the unique candidate is cut at m->t.
	g := mustGraph(t)
	if err := g.AddLinkType("a", Unidirectional); err != nil {
		t.Fatal(err)
	}
	if err := g.AddLinkType("b", Unidirectional); err != nil {
		t.Fatal(err)
	}
	addObjs(t, g, "s", "m", "t", "z")
	addLink(t, g, "e1", "a", "s", "m")
	addLink(t, g, "e2", "b", "m", "t")
	addLink(t, g, "e3", "b", "z", "t")
	const c CallerID = "u"
	grantAll(t, g, c, []string{"s", "m", "t", "z"}, "a")

	out, reason, m, err := g.ReachableFromTraced(context.Background(), "s", "t", c, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out != RestrictedUnknown || reason != ReasonCandidatesTruncated {
		t.Fatalf("unique path truncated: %s/%s", out, reason)
	}
	if m.LinksBlocked != 1 {
		t.Fatalf("exactly one blocked edge expected, got %d", m.LinksBlocked)
	}

	// z is outside anything reachable from s, so blocked edges cannot matter:
	// firm unreachable even though traversal is missing.
	if out, reason := classify(t, g, "s", "z", c); out != Unreachable || reason != ReasonNoGroundTruthPath {
		t.Fatalf("disconnected component: %s/%s", out, reason)
	}

	g.GrantTraversal(c, "b")
	if out, _ := classify(t, g, "s", "t", c); out != Reachable {
		t.Fatalf("after grant b: got %s", out)
	}
	g.RevokeTraversal(c, "b")
	if out, _ := classify(t, g, "s", "t", c); out != RestrictedUnknown {
		t.Fatalf("after revoke b: got %s", out)
	}
}

func TestDirectionAsymmetryOnSwap(t *testing.T) {
	g := mustGraph(t)
	addObjs(t, g, "a", "b")
	addLink(t, g, "l1", lt, "a", "b")
	const c CallerID = "u"
	grantAll(t, g, c, []string{"a", "b"}, lt)

	if out, _ := classify(t, g, "a", "b", c); out != Reachable {
		t.Fatalf("forward directed edge: got %s", out)
	}
	if out, reason := classify(t, g, "b", "a", c); out != Unreachable || reason != ReasonNoGroundTruthPath {
		t.Fatalf("reverse directed edge: %s/%s", out, reason)
	}

	addLink(t, g, "l2", bi, "a", "b")
	g.GrantTraversal(c, bi)
	if out, _ := classify(t, g, "b", "a", c); out != Reachable {
		t.Fatalf("bidirectional reverse: got %s", out)
	}
	g.RevokeTraversal(c, bi)
	if out, reason := classify(t, g, "b", "a", c); out != RestrictedUnknown || reason != ReasonCandidatesTruncated {
		t.Fatalf("reverse after bi revoke: %s/%s", out, reason)
	}
}

func TestSnapshotFixedDuringQuery(t *testing.T) {
	g := mustGraph(t)
	if err := g.AddLinkType("a", Unidirectional); err != nil {
		t.Fatal(err)
	}
	if err := g.AddLinkType("b", Unidirectional); err != nil {
		t.Fatal(err)
	}
	addObjs(t, g, "s", "m", "t")
	addLink(t, g, "e1", "a", "s", "m")
	addLink(t, g, "e2", "b", "m", "t")
	const c CallerID = "u"
	grantAll(t, g, c, []string{"s", "m", "t"}, "a", "b")

	started := make(chan struct{})
	released := make(chan struct{})
	revoked := make(chan struct{})
	var once sync.Once
	tr := &hookTracer{
		hook: func(ev map[string]any) {
			if ev["stage"] == "dequeue" && ev["node"] == "s" {
				once.Do(func() {
					close(started)
					// Revoke both link types after the query linearized.
					g.RevokeTraversal(c, "a")
					g.RevokeTraversal(c, "b")
					close(revoked)
				})
				<-released
			}
		},
	}

	var (
		out Outcome
		wg  sync.WaitGroup
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		var err error
		out, _, _, err = g.ReachableFromTraced(context.Background(), "s", "t", c, tr)
		if err != nil {
			t.Errorf("query: %v", err)
		}
	}()

	<-started
	<-revoked
	// A query linearized after the revocation must not see the permissions.
	if after, _ := classify(t, g, "s", "t", c); after != RestrictedUnknown {
		t.Fatalf("post-revoke query must not see permission: got %s", after)
	}
	close(released)
	wg.Wait()
	if out != Reachable {
		t.Fatalf("in-flight query keeps its snapshot, got %s", out)
	}
}

func TestConcurrentQueriesAndGrantsAreRaceFree(t *testing.T) {
	g := mustGraph(t)
	addObjs(t, g, "a", "b", "c")
	addLink(t, g, "l1", lt, "a", "b")
	addLink(t, g, "l2", lt, "b", "c")
	const c CallerID = "u"
	g.GrantExistence(c, "a")
	g.GrantExistence(c, "b")
	g.GrantExistence(c, "c")

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 200 {
				out, err := g.ReachableFrom(context.Background(), "a", "c", c)
				if err != nil {
					t.Error(err)
					return
				}
				switch out {
				case Reachable, RestrictedUnknown, Unreachable:
				default:
					t.Errorf("bad outcome %d", out)
				}
			}
		}()
	}
	for i := range 4 {
		wg.Add(1)
		go func(grant bool) {
			defer wg.Done()
			for range 200 {
				if grant {
					g.GrantTraversal(c, lt)
				} else {
					g.RevokeTraversal(c, lt)
				}
				grant = !grant
			}
		}(i%2 == 0)
	}
	wg.Wait()
}

func TestMetricsDoNotGrowWithForbiddenRegions(t *testing.T) {
	measure := func(n int) Metrics {
		g := mustGraph(t)
		addObjs(t, g, "s", "t")
		// A single lt edge s->t exists and is traversable. The huge region is
		// appended behind bidirectional links of a type never granted to u;
		// moreover its objects are invisible to u.
		addLink(t, g, "e0", lt, "s", "t")
		prev := "s"
		for i := range n {
			id := "x" + itoa(i)
			addObjs(t, g, id)
			addLink(t, g, "hx"+itoa(i), bi, prev, id)
			prev = id
		}
		const c CallerID = "u"
		g.GrantExistence(c, "s")
		g.GrantExistence(c, "t")
		g.GrantTraversal(c, lt)

		_, _, m, err := g.ReachableFromTraced(context.Background(), "s", "t", c, nil)
		if err != nil {
			t.Fatal(err)
		}
		return *m
	}

	small := measure(5)
	large := measure(5000)

	// Certified counters count only work the caller was allowed to attempt and
	// must stay exactly flat as the forbidden region grows 1000x.
	if small.ObjectsDequeued != large.ObjectsDequeued ||
		small.LinksInspected != large.LinksInspected ||
		small.LinksBlocked != large.LinksBlocked {
		t.Fatalf("certified work grew with forbidden region: small=%+v large=%+v", small, large)
	}
	if large.ObjectsDequeued > 2 || large.LinksInspected > 2 {
		t.Fatalf("certified work not bounded: %+v", large)
	}
	if large.ObjectsInvisible != 0 {
		t.Fatalf("invisible forbidden objects must never be inspected, got %d", large.ObjectsInvisible)
	}
	// The privileged-component "shadow" counters are a separate internal
	// metric; here the forbidden chain hangs off s through a type whose edge
	// is not held, so the certified BFS never enqueues its heads. Shadow BFS
	// (ground-truth disambiguation) is not even needed because t is reached.
	if large.ShadowObjectsDequeued != 0 || large.ShadowLinksInspected != 0 {
		t.Fatalf("shadow search ran despite certified hit: %+v", large)
	}
}

func TestMetricsCertifiedFlatWhenUnreachableBehindBlock(t *testing.T) {
	// Target genuinely unreachable: certified BFS must still stop at the one
	// blocked frontier edge, and the shadow BFS cost is restricted to the
	// ground-truth component of start. Append a second huge component that is
	// disconnected from start: neither metric may touch it.
	measure := func(n int) Metrics {
		g := mustGraph(t)
		addObjs(t, g, "s", "m", "t")
		addLink(t, g, "e1", lt, "s", "m")
		addLink(t, g, "e2", lt, "m", "t") // one chain to t (truncated -> unknown)
		// Disconnected huge component.
		prev := "q0"
		addObjs(t, g, prev)
		for i := range n {
			id := "q" + itoa(i+1)
			addObjs(t, g, id)
			addLink(t, g, "qe"+itoa(i), lt, prev, id)
			prev = id
		}
		const c CallerID = "u"
		grantAll(t, g, c, []string{"s", "m", "t", "q0"}) // visible, no traversal
		_, _, m, err := g.ReachableFromTraced(context.Background(), "s", "q0", c, nil)
		if err != nil {
			t.Fatal(err)
		}
		return *m
	}
	small := measure(10)
	large := measure(2000)
	if small.ObjectsDequeued != large.ObjectsDequeued {
		t.Fatalf("certified dequeues grew: %d vs %d", small.ObjectsDequeued, large.ObjectsDequeued)
	}
	// Shadow BFS may touch only the {s,m,t} component; the q-chain is separate.
	if large.ShadowObjectsDequeued != 3 {
		t.Fatalf("shadow BFS must span s component {s,m,t} only, not q-chain: %+v", large)
	}
}
