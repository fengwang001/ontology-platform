// Package api is the public DAG minimum-path-cover entry point.
package api

import (
	"fmt"

	"ontology/dag"
	"ontology/mpc"
)

// API wraps one fixed-size in-memory DAG; all methods are concurrency-safe.
type API struct{ g *dag.DAG }

// New fixes the node set to [0, n); n <= 0 fails before any state exists.
func New(n int) (*API, error) {
	g, err := dag.New(n)
	if err != nil {
		return nil, err
	}
	return &API{g}, nil
}

func (a *API) AddEdge(u, v int) error       { return a.g.AddEdge(u, v) }
func (a *API) EdgeCount() int               { return a.g.EdgeCount() }
func (a *API) Solve() (int, [][]int, error) { return mpc.Cover(a.g) }

// SelfCheck verifies the four invariants on built-in graphs; it is stateless.
func (a *API) SelfCheck() error {
	for _, c := range builtinCases {
		g, _ := dag.New(c.n)
		es := map[[2]int]bool{}
		for _, e := range c.edges {
			_ = g.AddEdge(e[0], e[1])
			es[e] = true
		}
		cover, paths, err := mpc.Cover(g)
		if err != nil {
			return err
		}
		if err := legal(c.n, es, cover, paths); err != nil {
			return err
		}
		_, adj := g.Snapshot()
		if cover != c.n-bruteMatching(adj) {
			return fmt.Errorf("cover=%d != n-|M| on %v", cover, c)
		}
		if c.n <= 4 && cover != bruteChains(c.n, es) {
			return fmt.Errorf("cover %d not minimal on %v", cover, c)
		}
	}
	return nil
}

type graphCase struct {
	n     int
	edges [][2]int
}

// builtinCases: isolated graphs, chains/forks, complete DAG, n=6 NOTES graph,
// and its n=7 variant with isolated vertex 6.
var builtinCases = []graphCase{
	{1, nil},
	{2, nil}, {2, [][2]int{{0, 1}}},
	{3, nil}, {3, [][2]int{{0, 2}, {0, 1}}},
	{3, [][2]int{{0, 1}, {1, 2}}},
	{3, [][2]int{{0, 1}, {0, 2}, {1, 2}}},
	{4, nil}, {4, [][2]int{{0, 1}, {1, 2}, {2, 3}}},
	{4, [][2]int{{0, 2}, {0, 3}, {1, 3}}}, {4, [][2]int{{0, 1}, {0, 2}, {0, 3}, {1, 2}, {1, 3}, {2, 3}}},
	{6, [][2]int{{0, 1}, {0, 2}, {2, 1}, {3, 4}, {4, 5}}}, {7, [][2]int{{0, 1}, {0, 2}, {2, 1}, {3, 4}, {4, 5}}},
}

// legal verifies invariant 1: real chains, pairwise disjoint, every
// vertex covered exactly once, len(paths) == cover.
func legal(n int, es map[[2]int]bool, cover int, paths [][]int) error {
	if cover != len(paths) {
		return fmt.Errorf("cover %d != %d paths", cover, len(paths))
	}
	seen := make([]bool, n)
	for _, p := range paths {
		for k, v := range p {
			if v < 0 || v >= n || seen[v] {
				return fmt.Errorf("bad vertex %d", v)
			}
			if k > 0 && !es[[2]int{p[k-1], v}] {
				return fmt.Errorf("missing edge %d->%d", p[k-1], v)
			}
			seen[v] = true
		}
	}
	for v := range seen {
		if !seen[v] {
			return fmt.Errorf("vertex %d uncovered", v)
		}
	}
	return nil
}

// bruteMatching enumerates all matchings independently of Kuhn (invariant 3).
func bruteMatching(adj [][]int) int {
	n, best := len(adj), 0
	used := make([]bool, n)
	var rec func(u, sz int)
	rec = func(u, sz int) {
		if u == n {
			best = max(best, sz)
			return
		}
		rec(u+1, sz)
		for _, v := range adj[u] {
			if !used[v] {
				used[v] = true
				rec(u+1, sz+1)
				used[v] = false
			}
		}
	}
	rec(0, 0)
	return best
}

// bruteChains enumerates permutations and counts forced cuts (invariant 2).
func bruteChains(n int, es map[[2]int]bool) int {
	p := make([]int, n)
	for i := range p {
		p[i] = i
	}
	best := n
	var rec func(k int)
	rec = func(k int) {
		if k == n {
			c := 1
			for i := 0; i+1 < n; i++ {
				if !es[[2]int{p[i], p[i+1]}] {
					c++
				}
			}
			best = min(best, c)
			return
		}
		for j := k; j < n; j++ {
			p[k], p[j] = p[j], p[k]
			rec(k + 1)
			p[k], p[j] = p[j], p[k]
		}
	}
	rec(0)
	return best
}
