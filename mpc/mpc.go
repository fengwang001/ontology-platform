// Package mpc computes a minimum vertex-disjoint path cover of a DAG: it
// builds the left/right split bipartite graph, finds a maximum matching with
// Kuhn augmenting paths, and reconstructs the chains. It depends only on dag.
package mpc

import "ontology/dag"

// matcher holds a bipartite matching over the split graph: left u and right u
// are copies of the same DAG vertex; edge u->v becomes left-u to right-v.
type matcher struct {
	adj    [][]int
	matchL []int // left u -> matched right node, -1 if unmatched
	matchR []int // right v -> matched left node, -1 if unmatched
	// probes counts right-node occupancy checks during the latest augmentation.
	// Unexported on purpose: it never crosses a public boundary; white-box tests
	// inside this package read it directly.
	probes int
}

func newMatcher(adj [][]int) *matcher {
	n := len(adj)
	l, r := make([]int, n), make([]int, n)
	for i := 0; i < n; i++ {
		l[i], r[i] = -1, -1
	}
	return &matcher{adj: adj, matchL: l, matchR: r}
}

// tryAugment runs one Kuhn augmenting-path search from left u.
func (s *matcher) tryAugment(u int) bool {
	s.probes = 0
	seen := make([]bool, len(s.matchR))
	var dfs func(int) bool
	dfs = func(x int) bool {
		for _, v := range s.adj[x] {
			if seen[v] {
				continue
			}
			seen[v] = true
			s.probes++ // one O(1) array lookup checks whether right v is taken
			if s.matchR[v] == -1 || dfs(s.matchR[v]) {
				s.matchR[v] = x
				s.matchL[x] = v
				return true
			}
		}
		return false
	}
	return dfs(u)
}

// maximumMatching finds a maximum matching by attempting one augmenting path
// per left node (Kuhn's algorithm).
func (s *matcher) maximumMatching() {
	for u := range s.adj {
		s.tryAugment(u)
	}
}

// Cover returns the minimum path-cover size and the concrete partition. A path
// starts at every vertex with no matched predecessor; an isolated vertex (no
// predecessor and no successor) starts a length-0 path of its own.
func Cover(g *dag.DAG) (int, [][]int, error) {
	if g.HasCycle() {
		return 0, nil, dag.ErrCycle
	}
	n, adj := g.Snapshot()
	s := newMatcher(adj)
	s.maximumMatching()
	matched := 0
	for _, l := range s.matchR {
		if l != -1 {
			matched++
		}
	}
	// Every matched edge joins what would otherwise be two paths, so the
	// minimum cover is n-|M|; buildPaths yields exactly that many chains.
	paths := buildPaths(n, s.matchL, s.matchR)
	return n - matched, paths, nil
}

// buildPaths walks from each predecessor-free start along matchL until -1.
// "No predecessor" alone defines a start, so isolated vertices are included.
func buildPaths(n int, matchL, matchR []int) [][]int {
	paths := make([][]int, 0, n)
	for start := 0; start < n; start++ {
		if matchR[start] != -1 {
			continue // has a predecessor: not a path start
		}
		p := []int{start}
		for v := matchL[start]; v != -1; v = matchL[v] {
			p = append(p, v)
		}
		paths = append(paths, p)
	}
	return paths
}

// ProbeBoundHolds reports whether, for several m values, a single augmentation
// on top of m pre-matched pairs checks an m-independent number of right nodes
// (at most one). Only the verdict is exposed, never the counter itself.
func ProbeBoundHolds() bool {
	for _, m := range []int{100, 1000, 10000} {
		adj := make([][]int, m+1)
		for i := 0; i < m; i++ {
			adj[i] = []int{i + 1} // left i pre-matched to right i+1
		}
		adj[m] = []int{0} // new left m reaches the one free right
		s := newMatcher(adj)
		for i := 0; i < m; i++ {
			s.matchL[i] = i + 1
			s.matchR[i+1] = i
		}
		if !s.tryAugment(m) || s.probes > 1 {
			return false
		}
	}
	return true
}
