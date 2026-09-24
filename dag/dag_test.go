package dag

import (
	"fmt"
	"testing"
)

// buildChain builds a chain of m unit-latency nodes n0→n1→…→n(m-1).
func buildChain(m int) *Graph {
	g := New()
	_ = g.Add("n0", 1)
	for i := 1; i < m; i++ {
		_ = g.Add(fmt.Sprintf("n%d", i), 1)
		_ = g.Link(fmt.Sprintf("n%d", i-1), fmt.Sprintf("n%d", i))
	}
	return g
}

// buildGraph builds a graph from names, latencies and edge index pairs.
func buildGraph(names []string, lats []int64, edges [][2]int) *Graph {
	g := New()
	for i := range names {
		_ = g.Add(names[i], lats[i])
	}
	for _, e := range edges {
		_ = g.Link(names[e[0]], names[e[1]])
	}
	return g
}

// A chain of m nodes has m-1 edges; a single topological pass relaxes each
// edge exactly once. Bellman-Ford-style vertex-round rescanning would be
// O(m^2); the counter must stay at m-1 for every scale.
func TestRelaxLinear(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		g := buildChain(m)
		g.Longest()
		if got := g.relax.Load(); got != int64(m-1) {
			t.Errorf("m=%d: relaxations = %d, want exactly %d (m-1, linear)", m, got, m-1)
		}
	}
}

// For every node, dist(v) must equal max(dist(u)+lat(v)) over its
// predecessors; sources get dist = own latency. pred(v) must achieve dist(v).
func TestDistDP(t *testing.T) {
	graphs := map[string]*Graph{
		"five-node": buildGraph(
			[]string{"S", "A", "A2", "B", "T"}, []int64{0, 30, 30, 50, 0},
			[][2]int{{0, 1}, {0, 3}, {1, 2}, {3, 4}, {2, 4}}),
		"chain-10": buildChain(10),
		"diamond": buildGraph(
			[]string{"s", "a", "b", "t"}, []int64{1, 1, 1, 1},
			[][2]int{{0, 1}, {0, 2}, {1, 3}, {2, 3}}),
	}
	for name, g := range graphs {
		dist, pred := g.Longest()
		for _, v := range g.Names() {
			want := g.lat[v] // source: own latency
			for i, u := range g.pre[v] {
				if d := dist[u] + g.lat[v]; i == 0 || d > want {
					want = d
				}
			}
			if dist[v] != want {
				t.Errorf("%s: dist(%s)=%d, want %d", name, v, dist[v], want)
			}
			p := pred[v]
			if (p == "") != (len(g.pre[v]) == 0) {
				t.Errorf("%s: pred(%s)=%q inconsistent with predecessor set", name, v, p)
			}
			if p != "" && dist[p]+g.lat[v] != dist[v] {
				t.Errorf("%s: pred(%s)=%q does not achieve dist", name, v, p)
			}
		}
	}
}
