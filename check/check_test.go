package check_test

import (
	"errors"
	"math"
	"math/rand"
	"sync"
	"testing"

	"ontology/dij"
	"ontology/graph"
)

type tc struct {
	name string
	n    int
	src  int
	es   []graph.Edge
	want []float64
	err  error
}

func bellman(n int, es []graph.Edge, src int) []float64 {
	d := make([]float64, n)
	for i := range d {
		d[i] = math.Inf(1)
	}
	d[src] = 0
	for range n - 1 {
		for _, e := range es {
			if d[e.From]+e.Weight < d[e.To] {
				d[e.To] = d[e.From] + e.Weight
			}
		}
	}
	return d
}

// badDijkstra intentionally processes stale heap records and overwrites the
// current distance with the stale value, violating the finalized-node rule.
func badDijkstra(n int, es []graph.Edge, src int) []float64 {
	g := graph.New(n)
	for _, e := range es {
		g.AddEdge(e.From, e.To, e.Weight)
	}
	d := make([]float64, n)
	for i := range d {
		d[i] = math.Inf(1)
	}
	d[src] = 0
	type item struct {
		u int
		w float64
	}
	q := []item{{src, 0}}
	for len(q) > 0 {
		best := 0
		for i := 1; i < len(q); i++ {
			if q[i].w < q[best].w {
				best = i
			}
		}
		cur := q[best]
		q = append(q[:best], q[best+1:]...)
		d[cur.u] = cur.w // overwrite with potentially stale distance
		for _, e := range g.Neighbors(cur.u) {
			if nd := cur.w + e.Weight; nd < d[e.To] {
				d[e.To] = nd
				q = append(q, item{e.To, nd})
			}
		}
	}
	return d
}

func eq(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func staleGraph() (int, []graph.Edge) {
	return 4, []graph.Edge{
		{0, 1, 4}, {0, 2, 1}, {2, 1, 1}, {1, 3, 1},
	}
}

func TestShortestPath(t *testing.T) {
	n, es := staleGraph()
	cases := []tc{
		{"single", 1, 0, nil, []float64{0}, nil},
		{"unreachable", 3, 0, []graph.Edge{{1, 2, 1}}, []float64{0, math.Inf(1), math.Inf(1)}, nil},
		{"stale improvements", n, 0, es, []float64{0, 2, 1, 3}, nil},
		{"zero weights", 2, 0, []graph.Edge{{0, 1, 0}}, []float64{0, 0}, nil},
		{"bad source low", 1, -1, nil, nil, dij.ErrBadSrc},
		{"bad source high", 1, 1, nil, nil, dij.ErrBadSrc},
		{"bad edge", 2, 0, []graph.Edge{{0, 2, 1}}, nil, dij.ErrBadEdge},
		{"negative edge", 2, 0, []graph.Edge{{0, 1, -1}}, nil, dij.ErrNegativeEdge},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := dij.ShortestPath(c.n, c.es, c.src)
			if !errors.Is(err, c.err) {
				t.Fatalf("err = %v, want %v", err, c.err)
			}
			if c.want != nil && !eq(got, c.want) {
				t.Fatalf("dist = %v, want %v", got, c.want)
			}
		})
	}
}

func TestBellmanReference(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for iter := 0; iter < 50; iter++ {
		n := 2 + r.Intn(30)
		es := make([]graph.Edge, 0, 4*n)
		for i := 0; i < 4*n; i++ {
			es = append(es, graph.Edge{r.Intn(n), r.Intn(n), float64(r.Intn(20))})
		}
		src := r.Intn(n)
		got, err := dij.ShortestPath(n, es, src)
		if err != nil {
			t.Fatal(err)
		}
		if want := bellman(n, es, src); !eq(got, want) {
			t.Fatalf("iter %d: got %v want %v", iter, got, want)
		}
	}
}

func TestStaleRecordMustBeSkipped(t *testing.T) {
	n, es := staleGraph()
	got, err := dij.ShortestPath(n, es, 0)
	if err != nil || !eq(got, []float64{0, 2, 1, 3}) {
		t.Fatalf("correct impl = %v, %v", got, err)
	}
	if bad := badDijkstra(n, es, 0); eq(bad, got) {
		t.Fatalf("buggy implementation unexpectedly correct: %v", bad)
	}
}

func TestRelaxationBound(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	n, m := 1000, 5000
	es := make([]graph.Edge, m)
	for i := range es {
		es[i] = graph.Edge{r.Intn(n), r.Intn(n), 1 + float64(r.Intn(100))}
	}
	before := dij.Relaxations()
	if _, err := dij.ShortestPath(n, es, 0); err != nil {
		t.Fatal(err)
	}
	if delta := dij.Relaxations() - before; delta > int64(m) {
		t.Fatalf("relaxations = %d, want <= %d", delta, m)
	}
}

func TestConcurrentPureCalls(t *testing.T) {
	n, es := staleGraph()
	var wg sync.WaitGroup
	for range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := dij.ShortestPath(n, es, 0)
			if err != nil || !eq(got, []float64{0, 2, 1, 3}) {
				t.Errorf("got %v, %v", got, err)
			}
		}()
	}
	wg.Wait()
}
