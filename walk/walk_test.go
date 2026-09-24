package walk

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"ontology/graph"
)

// sampleGraph: a->{b,c}, b->{d}, c->{d,e}, d->{a} (cycle), e->{e} (self
// loop), plus duplicate edge a->b and unreachable node z.
func sampleGraph() *graph.Graph {
	g := graph.New()
	g.AddEdge("a", "b")
	g.AddEdge("a", "c")
	g.AddEdge("a", "b") // duplicate
	g.AddEdge("b", "d")
	g.AddEdge("c", "d")
	g.AddEdge("c", "e")
	g.AddEdge("d", "a")
	g.AddEdge("e", "e")
	g.AddNode("z")
	return g
}

func starGraph(leaves int) *graph.Graph {
	g := graph.New()
	for i := 0; i < leaves; i++ {
		g.AddEdge("hub", fmt.Sprintf("leaf%05d", i))
	}
	return g
}

func TestDeterministicRepeatedRuns(t *testing.T) {
	g := sampleGraph()
	want, err := Walk(g, Initial("a"), 4)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		got, err := Walk(g, Initial("a"), 4)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(got.Visited, want.Visited) {
			t.Fatalf("run %d: got %v, want %v", i, got.Visited, want.Visited)
		}
	}
}

func TestVisitedCountEqualsMinBudgetReachable(t *testing.T) {
	g := sampleGraph()
	const reachable = 5 // a,b,c,d,e
	for budget := 0; budget <= reachable+2; budget++ {
		res, err := Walk(g, Initial("a"), budget)
		if err != nil {
			t.Fatal(err)
		}
		if want := min(budget, reachable); res.Stats.Visited != want || len(res.Visited) != want {
			t.Fatalf("budget %d: visited %d, want %d", budget, res.Stats.Visited, want)
		}
	}
}

func TestZeroBudgetIdempotent(t *testing.T) {
	g := sampleGraph()
	res, err := Walk(g, Initial("a"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Visited) != 0 || !reflect.DeepEqual(res.Next, Initial("a")) {
		t.Fatalf("zero budget: visited=%v next=%+v", res.Visited, res.Next)
	}
}

func TestDoneCheckpointResumesEmpty(t *testing.T) {
	g := sampleGraph()
	full, err := Walk(g, Initial("a"), 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	if !full.Next.Done() {
		t.Fatalf("expected done checkpoint, got %+v", full.Next)
	}
	res, err := Walk(g, full.Next, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Visited) != 0 || !res.Next.Done() {
		t.Fatalf("resume on done: visited=%v", res.Visited)
	}
}

func TestStarPeakQueueBound(t *testing.T) {
	g := starGraph(10000)
	w := newWalker(g, Initial("hub"))
	if err := w.run(100); err != nil {
		t.Fatal(err)
	}
	if w.peak > 4*100 {
		t.Fatalf("peak queue %d exceeds bound %d", w.peak, 4*100)
	}
	if len(w.order) != 100 {
		t.Fatalf("visited %d, want 100", len(w.order))
	}
}

func TestEdgesExaminedBoundedByOutDegree(t *testing.T) {
	g := sampleGraph()
	res, err := Walk(g, Initial("a"), 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	sum := 0
	for _, id := range res.Visited {
		sum += len(g.Out(id))
	}
	if res.Stats.EdgesExamined > sum {
		t.Fatalf("edges examined %d > out-degree sum %d", res.Stats.EdgesExamined, sum)
	}
}

func TestCyclesSelfLoopsDuplicatesVisitedOnce(t *testing.T) {
	g := sampleGraph()
	res, err := Walk(g, Initial("a"), 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, id := range res.Visited {
		if seen[id] {
			t.Fatalf("node %q visited twice in %v", id, res.Visited)
		}
		seen[id] = true
	}
	if seen["z"] {
		t.Fatal("unreachable node z visited")
	}
}

func TestStartMissingAndEmptyGraph(t *testing.T) {
	for _, g := range []*graph.Graph{graph.New(), sampleGraph()} {
		_, err := Walk(g, Initial("nope"), 5)
		if !errors.Is(err, ErrStartMissing) {
			t.Fatalf("got %v, want ErrStartMissing", err)
		}
	}
}

func TestNodeDeletedBetweenSegments(t *testing.T) {
	for _, victim := range []string{"a", "c"} {
		g := sampleGraph()
		first, err := Walk(g, Initial("a"), 1)
		if err != nil {
			t.Fatal(err)
		}
		g.RemoveNode(victim)
		_, err = Walk(g, first.Next, 10)
		if !errors.Is(err, ErrNodeGone) {
			t.Fatalf("delete %q: got %v, want ErrNodeGone", victim, err)
		}
		if !strings.Contains(err.Error(), `"`+victim+`"`) {
			t.Fatalf("error %q does not name node %q", err, victim)
		}
	}
}

func TestConcurrentResumeIdentical(t *testing.T) {
	g := sampleGraph()
	first, err := Walk(g, Initial("a"), 2)
	if err != nil {
		t.Fatal(err)
	}
	want, err := Walk(g, first.Next, 3)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := Walk(g, first.Next, 3)
			if err != nil {
				t.Error(err)
				return
			}
			if !slices.Equal(got.Visited, want.Visited) || got.Stats != want.Stats {
				t.Errorf("got %v %+v, want %v %+v", got.Visited, got.Stats, want.Visited, want.Stats)
			}
		}()
	}
	wg.Wait()
}
