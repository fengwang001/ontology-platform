package check_test

import (
	"cmp"
	"errors"
	"slices"
	"sync"
	"testing"

	"ontology/check"
	"ontology/dij"
	"ontology/graph"
)

var cases = []struct {
	n, src int
	edges  []graph.Edge
}{
	{1, 0, nil}, {4, 2, nil}, // single node; no edges
	{4, 0, []graph.Edge{{From: 0, To: 1, W: 1}, {From: 0, To: 2, W: 5}, {From: 1, To: 2, W: 1}, {From: 2, To: 3, W: 1}}},
}

func TestMatchesReference(t *testing.T) {
	for i, c := range cases {
		got, err := dij.ShortestPath(c.n, c.edges, c.src)
		if want := check.Naive(c.n, c.edges, c.src); err != nil || !slices.Equal(got, want) {
			t.Errorf("case %d: got %v want %v err %v", i, got, want, err)
		}
	}
}
func TestErrors(t *testing.T) {
	bad := []struct {
		n, src int
		edges  []graph.Edge
		want   error
	}{
		{2, 0, []graph.Edge{{From: 0, To: 1, W: -1}}, dij.ErrNegativeEdge},
		{2, 2, nil, dij.ErrBadSrc}, {2, 0, []graph.Edge{{From: 0, To: 2, W: 1}}, dij.ErrBadEdge},
	}
	for _, c := range bad {
		if _, err := dij.ShortestPath(c.n, c.edges, c.src); !errors.Is(err, c.want) {
			t.Errorf("got %v want %v", err, c.want)
		}
	}
}
func TestStaleEntries(t *testing.T) {
	c := cases[2] // node 2's dist improves twice, leaving a stale record
	got, err := dij.ShortestPath(c.n, c.edges, c.src)
	if err != nil || !slices.Equal(got, check.Naive(c.n, c.edges, c.src)) {
		t.Fatalf("dij got %v err %v", got, err)
	}
	if slices.Equal(buggy(c.n, c.edges, c.src), got) {
		t.Fatal("buggy impl without stale-skip should be wrong")
	}
}

// buggy applies every popped record without the stale check.
func buggy(n int, edges []graph.Edge, src int) []float64 {
	dist := check.Naive(n, nil, src)
	q := []graph.Edge{{From: src}}
	for len(q) > 0 {
		slices.SortFunc(q, func(a, b graph.Edge) int { return cmp.Compare(a.W, b.W) })
		cur := q[0]
		q = q[1:]
		dist[cur.From] = cur.W // bug: stale record overwrites better dist
		for _, e := range edges {
			if e.From == cur.From && cur.W+e.W < dist[e.To] {
				dist[e.To] = cur.W + e.W
				q = append(q, graph.Edge{From: e.To, W: dist[e.To]})
			}
		}
	}
	return dist
}
func TestRelaxationBound(t *testing.T) {
	const n, m = 1000, 5000
	edges := make([]graph.Edge, m)
	for i := range edges {
		edges[i] = graph.Edge{From: i % n, To: (i*7 + 1) % n, W: float64(i%13) + 0.5}
	}
	base := dij.Relaxations()
	_, _ = dij.ShortestPath(n, edges, 0)
	if got := dij.Relaxations() - base; got > m {
		t.Fatalf("relaxations %d exceed m=%d", got, m)
	}
}
func TestConcurrent(t *testing.T) {
	want, _ := dij.ShortestPath(cases[2].n, cases[2].edges, cases[2].src)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got, _ := dij.ShortestPath(cases[2].n, cases[2].edges, cases[2].src); !slices.Equal(got, want) {
				t.Error("mismatch")
			}
		}()
	}
	wg.Wait()
}
