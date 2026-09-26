// Package mpc computes a minimum vertex-disjoint path cover of a DAG by
// reducing it to maximum bipartite matching (cover = n - |M|). It depends
// only on the dag package.
package mpc

import (
	"sort"

	"ontology/dag"
)

// Result is a minimum path cover: Cover vertex-disjoint Paths whose union is
// exactly the whole node set.
type Result struct {
	Cover int
	Paths [][]int
}

type solver struct {
	n          int
	adj        [][]int // left u -> candidate right nodes v, ascending
	matchRight []int   // right v -> left u matched into it (-1 = free)
	matchLeft  []int   // left u -> right v matched out of it (-1 = free)
	// probeCount counts, for the most recent single augmenting attempt, how
	// many right nodes had their matched state inspected. Each inspection is
	// one indexed matchRight lookup (O(1)); the field is unexported and never
	// surfaces through any public method.
	probeCount int
}

// Solve verifies the DAG precondition and returns its minimum path cover.
func Solve(g *dag.Graph) (Result, error) {
	if !g.Acyclic() {
		return Result{}, dag.ErrCyclic
	}
	s := newSolver(g)
	for u := 0; u < s.n; u++ {
		s.tryAugment(u)
	}
	return s.result(), nil
}

func newSolver(g *dag.Graph) *solver {
	n := g.N()
	edges := g.Edges()
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].U != edges[j].U {
			return edges[i].U < edges[j].U
		}
		return edges[i].V < edges[j].V
	})
	adj := make([][]int, n)
	for _, e := range edges {
		adj[e.U] = append(adj[e.U], e.V)
	}
	mr := make([]int, n)
	ml := make([]int, n)
	for i := 0; i < n; i++ {
		mr[i], ml[i] = -1, -1
	}
	return &solver{n: n, adj: adj, matchRight: mr, matchLeft: ml}
}

func (s *solver) tryAugment(root int) bool {
	seen := make([]bool, s.n)
	s.probeCount = 0
	return s.augment(root, seen)
}

// augment is Kuhn's DFS with reassignment (backtracking), so the result is a
// maximum, not merely maximal, matching.
func (s *solver) augment(u int, seen []bool) bool {
	for _, v := range s.adj[u] {
		if seen[v] {
			continue
		}
		seen[v] = true
		s.probeCount++ // one direct matchRight[v] index test: O(1)
		w := s.matchRight[v]
		if w == -1 || s.augment(w, seen) {
			s.matchRight[v] = u
			s.matchLeft[u] = v
			return true
		}
	}
	return false
}

// result rebuilds chains. A node with no matched incoming edge (matchRight[v]
// == -1) is a path start; isolated nodes have neither in- nor outgoing match
// and therefore become one-element paths, so none are dropped.
func (s *solver) result() Result {
	paths := make([][]int, 0, s.n)
	for v := 0; v < s.n; v++ {
		if s.matchRight[v] != -1 {
			continue
		}
		path := make([]int, 0)
		for u := v; u != -1; u = s.matchLeft[u] {
			path = append(path, u)
		}
		paths = append(paths, path)
	}
	return Result{Cover: len(paths), Paths: paths}
}
