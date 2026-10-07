package ontology

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"
)

// normalizePaths turns path slices into a comparable multiset where the
// terminal reason is part of the path identity.
func normalizePaths(paths []Path) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = fmt.Sprintf("%v|%s|%v", p.Nodes, p.Reason, p.Links)
	}
	sort.Strings(out)
	return out
}

// Random graphs with self loops, parallel links, diamonds and long cycles:
// production traversal must match the independently maintained naive
// ancestor-sequence implementation path for path, reason for reason.
func TestNaiveDifferentialOnRandomGraphs(t *testing.T) {
	rng := rand.New(rand.NewSource(42))

	for iter := 0; iter < 200; iter++ {
		n := 2 + rng.Intn(10)
		objects := make([]ObjectID, n)
		for i := range objects {
			objects[i] = ObjectID(fmt.Sprintf("o%d", i))
		}

		typeCount := 1 + rng.Intn(2)
		directions := map[LinkType]Direction{}
		var links []Link
		for i := 0; i < typeCount; i++ {
			lt := LinkType(fmt.Sprintf("t%d", i))
			var dir Direction
			if rng.Intn(2) == 0 {
				dir = DirectionOut
			} else {
				dir = DirectionIn
			}
			directions[lt] = dir

			// Sparse-but-sticky random edges: enough density to produce
			// self loops, parallel links, diamonds and cycles without
			// exponential path explosion in the all-paths output.
			edgeCount := n + rng.Intn(2*n)
			for e := 0; e < edgeCount; e++ {
				src := objects[rng.Intn(n)]
				dst := objects[rng.Intn(n)]
				links = append(links, Link{
					ID:     LinkID(fmt.Sprintf("l-%d-%d-%d", i, e, rng.Intn(100000))),
					Type:   lt,
					Source: src,
					Target: dst,
				})
			}
		}

		g := NewMemGraph()
		for _, o := range objects {
			mustAdd(t, g, o)
		}
		for lt := range directions {
			g.DefineLinkType(lt)
		}
		for _, l := range links {
			if err := g.AddLink(l); err != nil {
				t.Fatal(err)
			}
		}

		req := TraverseRequest{
			Start:     objects[rng.Intn(n)],
			LinkTypes: directions,
			MaxDepth:  1 + rng.Intn(4),
		}

		got, err := TraverseOnSnapshot(g.Snapshot(), req)
		if err != nil {
			t.Fatalf("iter %d: unexpected error %v", iter, err)
		}
		want, err := NaiveTraverse(g.Snapshot(), req)
		if err != nil {
			t.Fatalf("iter %d: naive error %v", iter, err)
		}

		if !reflect.DeepEqual(normalizePaths(got.Paths), normalizePaths(want.Paths)) {
			t.Fatalf("iter %d mismatch\n got=%v\nwant=%v\nreq=%+v",
				iter, normalizePaths(got.Paths), normalizePaths(want.Paths), req)
		}
	}
}

// While a traversal runs against a pinned snapshot, another goroutine
// mutates the graph aggressively. The result must be exactly the result on
// the pinned snapshot (no mixed-in later edges, no duplicates, no omissions).
func TestConcurrentMutationSnapshotIsolation(t *testing.T) {
	g := NewMemGraph()
	for _, id := range []ObjectID{"a", "b", "c"} {
		mustAdd(t, g, id)
	}
	g.DefineLinkType("t")
	for _, l := range []Link{
		{ID: "base1", Type: "t", Source: "a", Target: "b"},
		{ID: "base2", Type: "t", Source: "b", Target: "c"},
		{ID: "base3", Type: "t", Source: "c", Target: "a"},
	} {
		if err := g.AddLink(l); err != nil {
			t.Fatal(err)
		}
	}

	snap := g.Snapshot()
	req := TraverseRequest{Start: "a", LinkTypes: outOnly("t"), MaxDepth: 8}

	// Expected output is computed now against the pinned snapshot and
	// independently recomputed by the naive oracle below.
	want, err := TraverseOnSnapshot(snap, req)
	if err != nil {
		t.Fatal(err)
	}
	wantNaive, err := NaiveTraverse(snap, req)
	if err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		i := 0
		for {
			select {
			case <-stop:
				return
			default:
			}
			id := LinkID(fmt.Sprintf("mut-%d", i))
			_ = g.AddLink(Link{ID: id, Type: "t", Source: "a", Target: "c"})
			_ = g.DeleteLink(id)
			i++
		}
	}()

	for run := 0; run < 50; run++ {
		got, err := TraverseOnSnapshot(snap, req)
		if err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		if !reflect.DeepEqual(normalizePaths(got.Paths), normalizePaths(want.Paths)) {
			t.Fatalf("run %d snapshot result drifted under mutation:\n got=%v\nwant=%v",
				run, normalizePaths(got.Paths), normalizePaths(want.Paths))
		}
		if !reflect.DeepEqual(normalizePaths(got.Paths), normalizePaths(wantNaive.Paths)) {
			t.Fatalf("run %d diverged from naive oracle", run)
		}
	}
	close(stop)
	wg.Wait()
}

// Verifiable property: ancestor membership probing is one O(1) map lookup
// per candidate edge, so the probe count of a traversal equals exactly the
// number of edges the walk expanded. Growing the graph outside the traversal
// footprint must not change the probe count, while the naive linear scan
// demonstrably spends extra probes per occurrence of ancestors.
func TestAncestorCheckCountIsLocalAndConstantPerEdge(t *testing.T) {
	// Small base graph: a -> b -> a (cycle), independent of a large tail.
	base := func() *MemGraph {
		g := NewMemGraph()
		mustAdd(t, g, "a")
		mustAdd(t, g, "b")
		g.DefineLinkType("t")
		mustAddLink(t, g, Link{ID: "e1", Type: "t", Source: "a", Target: "b"})
		mustAddLink(t, g, Link{ID: "e2", Type: "t", Source: "b", Target: "a"})
		return g
	}

	req := TraverseRequest{Start: "a", LinkTypes: outOnly("t"), MaxDepth: 10}

	g1 := base()
	resSmall, err := TraverseOnSnapshot(g1.Snapshot(), req)
	if err != nil {
		t.Fatal(err)
	}

	// Add many objects and edges that are unreachable from a: the traversal
	// footprint and probe count must remain identical.
	g2 := base()
	for i := 0; i < 500; i++ {
		id := ObjectID(fmt.Sprintf("x%d", i))
		mustAdd(t, g2, id)
		mustAddLink(t, g2, Link{
			ID:     LinkID(fmt.Sprintf("x%d", i)),
			Type:   "t",
			Source: id,
			Target: id,
		})
	}
	resLarge, err := TraverseOnSnapshot(g2.Snapshot(), req)
	if err != nil {
		t.Fatal(err)
	}

	if resSmall.AncestorChecks != resLarge.AncestorChecks {
		t.Fatalf("probe count must not depend on unrelated graph size: small=%d large=%d",
			resSmall.AncestorChecks, resLarge.AncestorChecks)
	}

	// Exact probe accounting: two candidate edges are expanded (e1 at a, e2
	// at b); each performs exactly one ancestor probe.
	if resSmall.AncestorChecks != 2 {
		t.Fatalf("want exactly 2 ancestor probes, got %d", resSmall.AncestorChecks)
	}

	// Invariant on every random graph: probe count == number of expanded
	// edges (== sum over emitted paths of len(Links)).
	rng := rand.New(rand.NewSource(7))
	for iter := 0; iter < 100; iter++ {
		g := randomTraversalGraph(t, rng)
		req := randomTraversalRequest(t, g, rng)
		res, err := TraverseOnSnapshot(g.Snapshot(), req)
		if err != nil {
			t.Fatalf("iter %d: %v", iter, err)
		}
		prefixes := map[string]struct{}{}
		for _, p := range res.Paths {
			for k := 1; k <= len(p.Links); k++ {
				prefixes[prefixKey(p.Links[:k])] = struct{}{}
			}
		}
		if res.AncestorChecks != len(prefixes) {
			t.Fatalf("iter %d: probes=%d but distinct expanded edges=%d", iter, res.AncestorChecks, len(prefixes))
		}
	}
}

func prefixKey(links []LinkID) string {
	key := ""
	for i, l := range links {
		if i > 0 {
			key += "|"
		}
		key += string(l)
	}
	return key
}

func mustAddLink(t *testing.T, g *MemGraph, l Link) {
	if t != nil {
		t.Helper()
	}
	if err := g.AddLink(l); err != nil {
		fatal(t, "%v", err)
	}
}

// randomTraversalGraph and randomTraversalRequest construct valid random
// fixtures (all referenced objects/link types are registered).
func randomTraversalGraph(t *testing.T, rng *rand.Rand) *MemGraph {
	t.Helper()
	n := 2 + rng.Intn(9)
	g := NewMemGraph()
	objects := make([]ObjectID, n)
	for i := range objects {
		objects[i] = ObjectID(fmt.Sprintf("o%d", i))
		mustAdd(t, g, objects[i])
	}
	for ti := 0; ti < 1+rng.Intn(2); ti++ {
		lt := LinkType(fmt.Sprintf("t%d", ti))
		g.DefineLinkType(lt)
		for e := 0; e < n+rng.Intn(2*n); e++ {
			l := Link{
				ID:     LinkID(fmt.Sprintf("l-%d-%d-%d", ti, e, rng.Intn(1<<30))),
				Type:   lt,
				Source: objects[rng.Intn(n)],
				Target: objects[rng.Intn(n)],
			}
			if err := g.AddLink(l); err != nil {
				t.Fatal(err)
			}
		}
	}
	return g
}

func randomTraversalRequest(t *testing.T, g *MemGraph, rng *rand.Rand) TraverseRequest {
	t.Helper()
	var start ObjectID
	g.mu.Lock()
	for o := range g.state.Load().objects {
		start = o
		if rng.Intn(2) == 0 {
			break
		}
	}
	directions := map[LinkType]Direction{}
	for lt := range g.state.Load().types {
		directions[lt] = Direction(rng.Intn(2))
	}
	g.mu.Unlock()
	return TraverseRequest{Start: start, LinkTypes: directions, MaxDepth: 1 + rng.Intn(4)}
}
