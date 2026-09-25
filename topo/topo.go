// Package topo computes deterministic topological orders via three-color DFS.
package topo

import (
	"errors"
	"fmt"
	"sync/atomic"

	"ontology/graph"
)

// Sentinel errors, distinguishable with errors.Is.
var (
	ErrCycle   = errors.New("topo: cycle detected")
	ErrBadEdge = errors.New("topo: edge endpoint out of range")
)

var edgeVisits atomic.Int64

// EdgeVisits reports how many edges TopoSort scanned since ResetEdgeVisits.
func EdgeVisits() int64 { return edgeVisits.Load() }

// ResetEdgeVisits zeroes the edge-visit counter.
func ResetEdgeVisits() { edgeVisits.Store(0) }

// Three colors: white=unvisited, gray=on the DFS stack, black=finished.
const (
	white = iota
	gray
	black
)

// TopoSort returns a topological order of nodes 0..n-1 over edges u->v.
// It is deterministic and safe for concurrent use.
func TopoSort(n int, edges [][2]int) ([]int, error) {
	g := graph.New(n)
	for _, e := range edges {
		if e[0] < 0 || e[0] >= n || e[1] < 0 || e[1] >= n {
			return nil, fmt.Errorf("%w: %d -> %d with n=%d", ErrBadEdge, e[0], e[1], n)
		}
		g.AddEdge(e[0], e[1])
	}
	color := make([]int, n)
	order := make([]int, 0, n)
	var visit func(u int) error
	visit = func(u int) error {
		color[u] = gray
		for _, v := range g.Adj(u) {
			edgeVisits.Add(1)
			switch color[v] {
			case gray: // back edge into the current stack: a real cycle
				return fmt.Errorf("%w: node %d", ErrCycle, v)
			case white:
				if err := visit(v); err != nil {
					return err
				}
			}
		}
		color[u] = black
		order = append(order, u)
		return nil
	}
	for u := 0; u < n; u++ {
		if color[u] == white {
			if err := visit(u); err != nil {
				return nil, err
			}
		}
	}
	for i, j := 0, len(order)-1; i < j; i, j = i+1, j-1 {
		order[i], order[j] = order[j], order[i]
	}
	return order, nil
}
