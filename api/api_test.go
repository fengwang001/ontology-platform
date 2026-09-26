package api_test

import (
	"errors"
	"sync"
	"testing"

	"ontology/api"
	"ontology/dgraph"
)

var edges7 = [][2]int{{0, 1}, {0, 6}, {1, 2}, {1, 6}, {2, 3}, {2, 4}, {3, 5}, {4, 5}}

func build(t *testing.T, n int, edges [][2]int) *api.Engine {
	e, err := api.New(n)
	if err != nil {
		t.Fatal(err)
	}
	for _, ed := range edges {
		if err := e.AddEdge(ed[0], ed[1]); err != nil {
			t.Fatal(err)
		}
	}
	e.Compute()
	return e
}

func reachable(n int, edges [][2]int, skip int) []bool {
	adj := make([][]int, n)
	for _, e := range edges {
		adj[e[0]] = append(adj[e[0]], e[1])
	}
	vis := make([]bool, n)
	var dfs func(u int)
	dfs = func(u int) {
		vis[u] = true
		for _, v := range adj[u] {
			if v != skip && !vis[v] {
				dfs(v)
			}
		}
	}
	if skip != 0 {
		dfs(0)
	}
	return vis
}

func TestIDomTable(t *testing.T) {
	e := build(t, 7, edges7)
	for v, want := range []int{0, 0, 1, 2, 2, 2, 0} {
		if got, err := e.IDom(v); err != nil || got != want {
			t.Errorf("IDom(%d) = %d, %v; want %d", v, got, err, want)
		}
	}
	e8 := build(t, 8, edges7)
	if got, err := e8.IDom(7); err != nil || got != -1 || e8.Dominates(0, 7) {
		t.Errorf("unreachable 7: IDom=%d err=%v dominates=%v; want -1 nil false", got, err, e8.Dominates(0, 7))
	}
	if _, err := e.IDom(7); !errors.Is(err, dgraph.ErrNodeOutOfRange) {
		t.Errorf("IDom out of range: err = %v; want ErrNodeOutOfRange", err)
	}
}

func TestErrorsDistinctAndStateUnchanged(t *testing.T) {
	sentinels := []error{dgraph.ErrNonPositiveN, dgraph.ErrNodeOutOfRange, dgraph.ErrSelfLoop, dgraph.ErrDuplicateEdge}
	seen := map[error]bool{}
	for _, s := range sentinels {
		if seen[s] {
			t.Fatalf("sentinel %v reused", s)
		}
		seen[s] = true
	}
	if _, err := api.New(0); !errors.Is(err, dgraph.ErrNonPositiveN) {
		t.Fatalf("New(0): err = %v; want ErrNonPositiveN", err)
	}
	e := build(t, 7, edges7)
	bad := []struct {
		u, v int
		want error
	}{{0, 7, dgraph.ErrNodeOutOfRange}, {3, 3, dgraph.ErrSelfLoop}, {0, 1, dgraph.ErrDuplicateEdge}}
	for _, c := range bad {
		if err := e.AddEdge(c.u, c.v); !errors.Is(err, c.want) {
			t.Fatalf("AddEdge(%d,%d): err = %v; want %v", c.u, c.v, err, c.want)
		}
	}
	if e.EdgeCount() != len(edges7) {
		t.Fatal("rejected op mutated state")
	}
	if err := e.AddEdge(3, 4); err != nil {
		t.Fatalf("engine unusable after rejects: %v", err)
	}
}

func TestDominatesBruteForce(t *testing.T) {
	cases := []struct {
		n     int
		edges [][2]int
	}{
		{7, edges7},
		{5, [][2]int{{0, 1}, {1, 2}, {0, 2}, {2, 3}, {3, 4}, {4, 2}}}, // cycle
		{4, [][2]int{{0, 1}, {2, 3}}},                                 // 2,3 unreachable
	}
	for _, c := range cases {
		e := build(t, c.n, c.edges)
		reach := reachable(c.n, c.edges, -1)
		for a := 0; a < c.n; a++ {
			without := reachable(c.n, c.edges, a)
			for b := 0; b < c.n; b++ {
				if want := reach[b] && !without[b]; e.Dominates(a, b) != want {
					t.Fatalf("n=%d: Dominates(%d,%d) = %v; want %v", c.n, a, b, !want, want)
				}
			}
		}
	}
	if err := api.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentConsistent(t *testing.T) {
	e := build(t, 7, edges7)
	pairs := [][2]int{{0, 6}, {1, 6}, {2, 5}, {3, 5}, {0, 0}, {6, 6}, {5, 3}}
	results := make([][]bool, 16)
	var wg sync.WaitGroup
	for k := range results {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			for _, p := range pairs {
				results[k] = append(results[k], e.Dominates(p[0], p[1]))
			}
			_, _ = e.IDom(5)
			_ = e.EdgeCount()
		}(k)
	}
	wg.Wait()
	for k := 1; k < len(results); k++ {
		for j := range pairs {
			if results[k][j] != results[0][j] {
				t.Fatalf("goroutine %d pair %d: %v != %v", k, j, results[k][j], results[0][j])
			}
		}
	}
}
