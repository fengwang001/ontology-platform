// Package sw implements the Stoer-Wagner global minimum cut algorithm
// on top of package wg.
package sw

import (
	"container/heap"

	"ontology/wg"
)

// Phase describes one Stoer-Wagner phase.
type Phase struct {
	S, T   int   // last two nodes added by MAS (merged afterwards)
	Cut    int64 // cut value of this phase
	STEdge int64 // weight of the direct s-t edge, 0 if none
}

type ent struct {
	w int64
	v int
}

// maxHeap orders by weight descending, ties broken by smaller node id.
type maxHeap []ent

func (h maxHeap) Len() int { return len(h) }
func (h maxHeap) Less(i, j int) bool {
	if h[i].w != h[j].w {
		return h[i].w > h[j].w
	}
	return h[i].v < h[j].v
}
func (h maxHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *maxHeap) Push(x any)   { *h = append(*h, x.(ent)) }
func (h *maxHeap) Pop() any {
	old := *h
	e := old[len(old)-1]
	*h = old[:len(old)-1]
	return e
}

// masCounter records, per MAS run, the maximum number of heap pops
// ("candidate checks") needed to select one node. Non-exported on
// purpose: only white-box tests inside this package may read it.
type masCounter struct{ maxChecked int }

// mas runs one maximum adjacency search starting from node 0 over the
// alive nodes of adj, returning the last two added nodes and the cut
// of the phase (weight of t to A\{t}).
func mas(adj []map[int]int64, alive []bool, c *masCounter) (s, t int, cut int64) {
	n := len(adj)
	inA := make([]bool, n)
	w := make([]int64, n)
	h := &maxHeap{}
	left := 0
	for v := 0; v < n; v++ {
		if alive[v] {
			heap.Push(h, ent{0, v})
			left++
		}
	}
	s, t = -1, -1
	for k := 0; k < left; k++ {
		checked := 0
		var e ent
		for {
			e = heap.Pop(h).(ent)
			checked++
			if !inA[e.v] && e.w == w[e.v] { // stale entries skipped
				break
			}
		}
		if checked > c.maxChecked {
			c.maxChecked = checked
		}
		inA[e.v] = true
		s, t = t, e.v
		cut = w[e.v]
		for x, wx := range adj[e.v] {
			if alive[x] && !inA[x] {
				w[x] += wx
				heap.Push(h, ent{w[x], x})
			}
		}
	}
	return s, t, cut
}

// mergeInto contracts drop into keep, summing edges to common
// neighbors. The s-t edge itself disappears (it becomes internal).
func mergeInto(adj []map[int]int64, keep, drop int) {
	if adj[keep] == nil {
		adj[keep] = map[int]int64{}
	}
	for x, wx := range adj[drop] {
		if x == keep {
			continue
		}
		adj[keep][x] += wx
		adj[x][keep] += wx
		delete(adj[x], drop)
	}
	delete(adj[keep], drop)
	adj[drop] = nil
}

// Run executes all n-1 phases on a private copy of g and returns them
// in order. The supernode keeps the smaller id, so node 0 (the MAS
// start) is always alive.
func Run(g *wg.Graph) []Phase {
	adj := g.Adjacency()
	alive := make([]bool, g.N())
	for i := range alive {
		alive[i] = true
	}
	var phases []Phase
	for left := g.N(); left > 1; left-- {
		var c masCounter
		s, t, cut := mas(adj, alive, &c)
		phases = append(phases, Phase{S: s, T: t, Cut: cut, STEdge: adj[s][t]})
		keep, drop := s, t
		if drop < keep {
			keep, drop = drop, keep
		}
		mergeInto(adj, keep, drop)
		alive[drop] = false
	}
	return phases
}

// MinCut returns the global minimum cut value: the minimum over all
// phase cuts.
func MinCut(g *wg.Graph) int64 {
	best := int64(-1)
	for _, p := range Run(g) {
		if best < 0 || p.Cut < best {
			best = p.Cut
		}
	}
	return best
}
