// Package graph builds and validates a directed acyclic task graph.
package graph

import "errors"

// ErrUnknownDep reports an edge that references a missing task.
var ErrUnknownDep = errors.New("graph: dependency on unknown task")

// Graph is a directed graph: edge from -> to means "to depends on from",
// i.e. from must finish before to may start.
type Graph struct {
	nodes map[string]struct{}
	succ  map[string]map[string]struct{} // from -> successors
	pred  map[string]map[string]struct{} // to -> predecessors
	order []string                        // insertion order, deterministic
}

// New returns an empty graph.
func New() *Graph {
	return &Graph{
		nodes: map[string]struct{}{},
		succ:  map[string]map[string]struct{}{},
		pred:  map[string]map[string]struct{}{},
	}
}

// Add registers a task id. Re-adding an existing id is a no-op.
func (g *Graph) Add(id string) {
	if _, ok := g.nodes[id]; ok {
		return
	}
	g.nodes[id] = struct{}{}
	g.order = append(g.order, id)
	g.succ[id] = map[string]struct{}{}
	g.pred[id] = map[string]struct{}{}
}

// AddEdge records that to depends on from. Adding the same pair twice is
// idempotent. It returns ErrUnknownDep when either endpoint is missing.
func (g *Graph) AddEdge(from, to string) error {
	if _, ok := g.nodes[from]; !ok {
		return unknownDepError(from, to)
	}
	if _, ok := g.nodes[to]; !ok {
		return unknownDepError(from, to)
	}
	if _, dup := g.succ[from][to]; dup {
		return nil
	}
	g.succ[from][to] = struct{}{}
	g.pred[to][from] = struct{}{}
	return nil
}

// Has reports whether id is a registered node.
func (g *Graph) Has(id string) bool {
	_, ok := g.nodes[id]
	return ok
}

// Nodes returns all node ids in insertion order.
func (g *Graph) Nodes() []string {
	out := make([]string, len(g.order))
	copy(out, g.order)
	return out
}

// Successors returns the successors of id in sorted order.
func (g *Graph) Successors(id string) []string {
	return sortedKeys(g.succ[id])
}

// Predecessors returns the predecessors of id in sorted order.
func (g *Graph) Predecessors(id string) []string {
	return sortedKeys(g.pred[id])
}

// EdgeCount returns the number of distinct edges.
func (g *Graph) EdgeCount() int {
	n := 0
	for _, tos := range g.succ {
		n += len(tos)
	}
	return n
}
