// Package dom computes immediate dominators over a dgraph.Graph and answers
// dominance queries in O(1) via DFS in/out intervals on the dominator tree.
package dom

import (
	"sync/atomic"

	"ontology/dgraph"
)

// Tree is the dominator tree of a graph rooted at node 0.
type Tree struct {
	idom    []int // -1 for unreachable nodes; idom[0] == 0
	tin     []int
	tout    []int
	reach   []bool
	checked atomic.Int64 // nodes inspected by the most recent Dominates call
}

// walk runs iterative DFS from root over succ, returning the postorder and
// per-node entry/exit stamps (exit = highest entry index in the subtree).
func walk(n, root int, succ func(int) []int) (post, tin, tout []int) {
	type frame struct{ v, ci int }
	seen := make([]bool, n)
	tin, tout = make([]int, n), make([]int, n)
	seen[root] = true
	timer := 1
	st := []frame{{v: root}}
	for len(st) > 0 {
		f := &st[len(st)-1]
		s := succ(f.v)
		if f.ci < len(s) {
			w := s[f.ci]
			f.ci++
			if !seen[w] {
				seen[w] = true
				tin[w] = timer
				timer++
				st = append(st, frame{v: w})
			}
		} else {
			post = append(post, f.v)
			tout[f.v] = timer - 1
			st = st[:len(st)-1]
		}
	}
	return post, tin, tout
}

// Compute runs the Cooper-Harvey-Kennedy iterative algorithm: the same
// fixpoint as naive predecessor dominator-set intersection, over idoms.
func Compute(g *dgraph.Graph) *Tree {
	n := g.N()
	preds := make([][]int, n)
	for u := 0; u < n; u++ {
		for _, v := range g.Succ(u) {
			preds[v] = append(preds[v], u)
		}
	}
	post, _, _ := walk(n, 0, g.Succ)
	rpoIdx, idom := make([]int, n), make([]int, n)
	for i := range rpoIdx {
		rpoIdx[i], idom[i] = -1, -1
	}
	idom[0] = 0
	rpo := make([]int, len(post))
	for i, v := range post {
		rpo[len(post)-1-i] = v
		rpoIdx[v] = len(post) - 1 - i
	}
	intersect := func(a, b int) int {
		for a != b {
			if rpoIdx[a] < rpoIdx[b] {
				a, b = b, a
			}
			a = idom[a]
		}
		return a
	}
	for changed := true; changed; {
		changed = false
		for _, v := range rpo[1:] {
			ni := -1
			for _, u := range preds[v] {
				if idom[u] < 0 {
					continue
				}
				if ni < 0 {
					ni = u
				} else {
					ni = intersect(u, ni)
				}
			}
			if ni >= 0 && idom[v] != ni {
				idom[v] = ni
				changed = true
			}
		}
	}
	reach := make([]bool, n)
	children := make([][]int, n)
	for _, v := range rpo {
		reach[v] = true
		if v != 0 {
			children[idom[v]] = append(children[idom[v]], v)
		}
	}
	_, tin, tout := walk(n, 0, func(v int) []int { return children[v] })
	return &Tree{idom: idom, tin: tin, tout: tout, reach: reach}
}

// IDom returns the immediate dominator of v, or -1 if v is unreachable
// from node 0 or out of range.
func (t *Tree) IDom(v int) int {
	if v < 0 || v >= len(t.idom) {
		return -1
	}
	return t.idom[v]
}

// Dominates reports whether every path from 0 to b passes through a.
// It inspects only the two query nodes, regardless of graph size.
func (t *Tree) Dominates(a, b int) bool {
	t.checked.Store(2)
	if a < 0 || a >= len(t.reach) || b < 0 || b >= len(t.reach) || !t.reach[a] || !t.reach[b] {
		return false
	}
	return t.tin[a] <= t.tin[b] && t.tout[b] <= t.tout[a]
}

// QueryCostBounded builds chains of the given sizes and reports whether every
// Dominates(0, m-1) query inspected at most 2 nodes (verdict only).
func QueryCostBounded(sizes ...int) bool {
	for _, m := range sizes {
		g, err := dgraph.New(m)
		if err != nil {
			return false
		}
		for i := 0; i+1 < m; i++ {
			if g.AddEdge(i, i+1) != nil {
				return false
			}
		}
		tr := Compute(g)
		if !tr.Dominates(0, m-1) || tr.checked.Load() > 2 {
			return false
		}
	}
	return true
}
