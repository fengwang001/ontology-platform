// Package dag holds the view-dependency graph (cycle checks, dirty closure, topo order).
package dag

import (
	"container/heap"
	"errors"
	"sort"
)

// ErrCycle is returned when adding a view would introduce a cycle.
var ErrCycle = errors.New("dag: dependency cycle")

// Graph is an acyclic dependency graph: edges run dependency -> dependent.
type Graph struct {
	deps     map[string][]string // view -> unique deps in declaration order
	children map[string][]string // dep -> views that directly depend on it
}

// New returns an empty graph.
func New() *Graph {
	return &Graph{deps: map[string][]string{}, children: map[string][]string{}}
}

// HasView reports whether n is a declared view.
func (g *Graph) HasView(n string) bool { _, ok := g.deps[n]; return ok }

// Views returns all declared view names sorted lexicographically.
func (g *Graph) Views() []string {
	out := make([]string, 0, len(g.deps))
	for n := range g.deps {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Deps returns a copy of the declared direct dependencies of view v.
func (g *Graph) Deps(v string) []string { return append([]string(nil), g.deps[v]...) }

func dedupe(ds []string) []string {
	seen, out := map[string]bool{}, ds[:0:0]
	for _, d := range ds {
		if !seen[d] {
			seen[d], out = true, append(out, d)
		}
	}
	return out
}

// AddView registers v with deps ds. A self-edge or path closing back on v
// is rejected with ErrCycle and nothing is stored; the old graph is acyclic.
func (g *Graph) AddView(v string, ds []string) error {
	deps := dedupe(append([]string(nil), ds...))
	edges := func(n string) []string {
		if n == v {
			return deps // virtual edges of the not-yet-committed node
		}
		return g.deps[n]
	}
	seen := map[string]bool{}
	var reaches func(n string) bool
	reaches = func(n string) bool {
		for _, d := range edges(n) {
			if d == v {
				return true
			}
			if !seen[d] {
				seen[d] = true
				if reaches(d) {
					return true
				}
			}
		}
		return false
	}
	if reaches(v) {
		return ErrCycle
	}
	g.deps[v] = deps
	for _, d := range deps {
		g.children[d] = append(g.children[d], v)
	}
	return nil
}

// Descendants returns every view reachable from any root, following
// dependency -> dependent edges (the transitive closure of impact).
func (g *Graph) Descendants(roots map[string]bool) map[string]bool {
	dirty := map[string]bool{}
	q := make([]string, 0, len(roots))
	for r := range roots {
		q = append(q, r)
	}
	for len(q) > 0 {
		n := q[0]
		q = q[1:]
		for _, ch := range g.children[n] {
			if !dirty[ch] {
				dirty[ch] = true
				q = append(q, ch)
			}
		}
	}
	return dirty
}

type minHeap []string

func (h minHeap) Len() int           { return len(h) }
func (h minHeap) Less(i, j int) bool { return h[i] < h[j] }
func (h minHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *minHeap) Push(x any)        { *h = append(*h, x.(string)) }
func (h *minHeap) Pop() any {
	x := (*h)[len(*h)-1]
	*h = (*h)[:len(*h)-1]
	return x
}

// Order returns the dirty views in the mandated order: repeatedly the
// lexicographically smallest dirty view whose dirty dependencies are all
// already refreshed. This is a Kahn process restricted to the dirty set.
func (g *Graph) Order(dirty map[string]bool) []string {
	indeg := make(map[string]int, len(dirty))
	h := &minHeap{}
	for v := range dirty {
		for _, d := range g.deps[v] {
			if dirty[d] {
				indeg[v]++
			}
		}
		if indeg[v] == 0 {
			*h = append(*h, v)
		}
	}
	heap.Init(h)
	out := make([]string, 0, len(dirty))
	for h.Len() > 0 {
		v := heap.Pop(h).(string)
		out = append(out, v)
		for _, w := range g.children[v] {
			if dirty[w] {
				indeg[w]--
				if indeg[w] == 0 {
					heap.Push(h, w)
				}
			}
		}
	}
	return out
}
