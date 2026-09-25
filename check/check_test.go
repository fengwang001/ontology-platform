package check

import (
	"errors"
	"math"
	"math/rand"
	"sync"
	"testing"

	"ontology/dij"
	"ontology/graph"
)

func e(u, v int, w float64) graph.Edge { return graph.Edge{From: u, To: v, Weight: w} }

func infs(n, s int) []float64 { d := make([]float64, n); for i := 1; i < n; i++ { d[i] = math.Inf(1) }; return d }

func bf(n int, es []graph.Edge, s int) []float64 {
	d := infs(n, s)
	for k := 1; k < n; k++ {
		for _, x := range es {
			if c := d[x.From] + x.Weight; c < d[x.To] { d[x.To] = c }
		}
	}
	return d
}

func bad(n int, es []graph.Edge, s int) []float64 {
	d := infs(n, s)
	q := []int{s, 0}
	for d[s] = 0; len(q) > 0; {
		u, w := q[len(q)-2], q[len(q)-1]; q, d[u] = q[:len(q)-2], float64(w)
		for _, x := range es {
			if c := w + int(x.Weight); x.From == u && c < int(d[x.To]) { d[x.To], q = float64(c), append(q, x.To, c) }
		}
	}
	return d
}

func TestCheck(t *testing.T) {
	cases := []struct {
		name  string
		n, s  int
		edges []graph.Edge
		err   error
	}{
		{"single", 1, 0, nil, nil}, {"no edges", 3, 0, nil, nil},
		{"weighted", 4, 0, []graph.Edge{e(0, 1, 4), e(0, 2, 1), e(2, 1, 2), e(1, 3, 1)}, nil},
		{"repeated improvements", 4, 0, []graph.Edge{e(0, 1, 10), e(0, 2, 1), e(2, 1, 1), e(1, 3, 1)}, nil},
		{"bad source", 2, 2, nil, dij.ErrBadSrc},
		{"bad edge", 2, 0, []graph.Edge{e(0, 2, 1)}, dij.ErrBadEdge}, {"negative edge", 2, 0, []graph.Edge{e(0, 1, -1)}, dij.ErrNegativeEdge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := dij.ShortestPath(tc.n, tc.edges, tc.s)
			if !errors.Is(err, tc.err) || (tc.err == nil && !equal(got, bf(tc.n, tc.edges, tc.s))) { t.Fatalf("got %v err %v", got, err) }
		})
	}

	t.Run("stale records are skipped", func(t *testing.T) {
		es := []graph.Edge{e(0, 1, 10), e(0, 2, 1), e(2, 1, 1)}
		if got, _ := dij.ShortestPath(3, es, 0); got[1] != 2 || bad(3, es, 0)[1] == 2 { t.Fatal("stale handling") }
	})

	t.Run("relaxation bound", func(t *testing.T) {
		r := rand.New(rand.NewSource(1))
		es := []graph.Edge{}
		for i := 0; i < 5000; i++ { es = append(es, e(r.Intn(1000), r.Intn(1000), float64(r.Intn(100)+1))) }
		dij.ResetRelaxations()
		if _, err := dij.ShortestPath(1000, es, 0); err != nil || dij.Relaxations() > 5000 { t.Fatal("bound") }
	})

	t.Run("concurrent calls", func(t *testing.T) {
		var wg sync.WaitGroup
		for range 32 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				es := []graph.Edge{e(0, 1, 2), e(0, 2, 5), e(1, 2, 1)}
				if got, err := dij.ShortestPath(3, es, 0); err != nil || got[2] != 3 { t.Error("race") }
			}()
		}
		wg.Wait()
	})
}

func equal(a, b []float64) bool {
	for i := range a {
		if a[i] != b[i] { return false }
	}
	return len(a) == len(b)
}
