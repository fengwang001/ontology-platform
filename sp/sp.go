// Package sp computes pointwise-maximal solutions of difference-constraint
// systems as shortest paths from a super source, and detects negative cycles.
package sp

import (
	"errors"

	"ontology/dc"
)

// ErrNegativeCycle reports an infeasible system (a negative-weight cycle).
var ErrNegativeCycle = errors.New("sp: negative-weight cycle detected")

const inf = int64(1) << 60 // safely far from any reachable distance

type edge struct {
	to int
	w  int64
}

// Engine solves one fixed constraint set. It is built per Solve call and is
// not shared between goroutines, so it needs no locking.
type Engine struct {
	adj [][]edge
	src int // super source: vertex n, with x_i <= 0 via edges src->i of weight 0
	n   int
	// fullPasses counts whole-table relaxation sweeps. The queue-driven
	// solver below only ever relaxes edges out of active vertices, so this
	// stays 0; it exists so the white-box test can pin that property.
	fullPasses int
}

// NewEngine builds the graph: one edge u->v of weight w per constraint
// x_v - x_u <= w, plus zero-weight edges from the super source to every
// variable (x_i <= 0), which makes the shortest-path solution pointwise maximal.
func NewEngine(n int, cons []dc.Constraint) *Engine {
	adj := make([][]edge, n+1)
	for i := 0; i < n; i++ {
		adj[n] = append(adj[n], edge{to: i, w: 0})
	}
	for _, c := range cons {
		adj[c.U] = append(adj[c.U], edge{to: c.V, w: c.W})
	}
	return &Engine{adj: adj, src: n, n: n}
}

// Solve returns the shortest distances from the super source, i.e. the
// pointwise-maximal feasible assignment, or ErrNegativeCycle. Queue-driven
// Bellman-Ford: only vertices whose distance just dropped are expanded.
func (e *Engine) Solve() ([]int64, error) {
	nv := e.n + 1
	dist := make([]int64, nv)
	for i := range dist {
		dist[i] = inf
	}
	dist[e.src] = 0
	inQ := make([]bool, nv)
	plen := make([]int, nv) // edges on the path witnessing each distance
	queue := []int{e.src}
	inQ[e.src] = true
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		inQ[u] = false
		for _, ed := range e.adj[u] {
			if d := dist[u] + ed.w; d < dist[ed.to] {
				dist[ed.to] = d
				plen[ed.to] = plen[u] + 1
				if plen[ed.to] >= nv { // a shortest path uses < nv edges
					return nil, ErrNegativeCycle
				}
				if !inQ[ed.to] {
					inQ[ed.to] = true
					queue = append(queue, ed.to)
				}
			}
		}
	}
	return dist[:e.n], nil
}
