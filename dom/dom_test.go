package dom

import (
	"math/bits"
	"math/rand"
	"testing"

	"ontology/dgraph"
)

type graphCase struct {
	n     int
	edges [][2]int
}

func buildGraph(t *testing.T, n int, edges [][2]int) *dgraph.Graph {
	t.Helper()
	g, err := dgraph.New(n)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range edges {
		if err := g.AddEdge(e[0], e[1]); err != nil {
			t.Fatal(err)
		}
	}
	return g
}

func randomGraphs() []graphCase {
	rng := rand.New(rand.NewSource(750))
	var out []graphCase
	for _, n := range []int{10, 30, 60} {
		for k := 0; k < 2; k++ {
			var edges [][2]int
			for u := 0; u < n; u++ {
				for v := 0; v < n; v++ {
					if u != v && rng.Intn(100) < 8 {
						edges = append(edges, [2]int{u, v})
					}
				}
			}
			rng.Shuffle(len(edges), func(i, j int) { edges[i], edges[j] = edges[j], edges[i] })
			out = append(out, graphCase{n, edges})
		}
	}
	return out
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

// naiveIDom is the textbook fixpoint over predecessor dominator sets.
func naiveIDom(n int, edges [][2]int) []int {
	preds := make([][]int, n)
	for _, e := range edges {
		preds[e[1]] = append(preds[e[1]], e[0])
	}
	reach := reachable(n, edges, -1)
	var full uint64
	for v := 0; v < n; v++ {
		if reach[v] {
			full |= 1 << v
		}
	}
	dom := make([]uint64, n)
	for v := range dom {
		dom[v] = full
	}
	dom[0] = 1
	for changed := true; changed; {
		changed = false
		for v := 1; v < n; v++ {
			if !reach[v] {
				continue
			}
			inter := full
			for _, p := range preds[v] {
				if reach[p] {
					inter &= dom[p]
				}
			}
			if nv := inter | 1<<v; nv != dom[v] {
				dom[v] = nv
				changed = true
			}
		}
	}
	id := make([]int, n)
	for v := range id {
		id[v] = -1
		if !reach[v] {
			continue
		}
		best, sz := v, 0
		for d := 0; d < n; d++ {
			if d != v && dom[v]&(1<<d) != 0 && bits.OnesCount64(dom[d]) > sz {
				best, sz = d, bits.OnesCount64(dom[d])
			}
		}
		id[v] = best
	}
	return id
}

func TestNaiveConsistency(t *testing.T) {
	for _, c := range randomGraphs() {
		tr := Compute(buildGraph(t, c.n, c.edges))
		want := naiveIDom(c.n, c.edges)
		for v := 0; v < c.n; v++ {
			if got := tr.IDom(v); got != want[v] {
				t.Fatalf("n=%d: IDom(%d) = %d; want %d", c.n, v, got, want[v])
			}
		}
	}
}

func TestDominatesCheckedNodes(t *testing.T) {
	for _, m := range []int{100, 500, 2000, 10000} {
		var edges [][2]int
		for i := 0; i+1 < m; i++ {
			edges = append(edges, [2]int{i, i + 1})
		}
		tr := Compute(buildGraph(t, m, edges))
		if !tr.Dominates(0, m-1) {
			t.Fatalf("m=%d: 0 must dominate %d", m, m-1)
		}
		if got := tr.checked.Load(); got > 2 {
			t.Fatalf("m=%d: query inspected %d nodes; want <= 2", m, got)
		}
	}
}
