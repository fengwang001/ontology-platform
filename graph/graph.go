// Package graph provides an in-memory adjacency-list directed graph.
// Node IDs are strings; out-edges are kept sorted by target ID and
// deduplicated so that traversal order never depends on map iteration.
//
// A Graph is safe for concurrent reads. Mutating a Graph concurrently
// with reads is not supported; the walk/resume packages detect removal
// of nodes referenced by a resume token and report it as an error.
package graph

import "sort"

// Graph is a directed graph stored as sorted adjacency lists.
type Graph struct {
	edges map[string][]string
}

// New returns an empty graph.
func New() *Graph {
	return &Graph{edges: make(map[string][]string)}
}

// AddNode registers id as a node, even if it has no edges.
func (g *Graph) AddNode(id string) {
	if _, ok := g.edges[id]; !ok {
		g.edges[id] = nil
	}
}

// AddEdge adds a directed edge from -> to. Both endpoints are registered.
// Duplicate edges are collapsed; self-loops are kept exactly once.
// Out-edges remain sorted by target ID after every insertion.
func (g *Graph) AddEdge(from, to string) {
	g.AddNode(to)
	out := g.edges[from]
	i := sort.SearchStrings(out, to)
	if i < len(out) && out[i] == to {
		return // duplicate edge: adjacency is a set
	}
	out = append(out, "")
	copy(out[i+1:], out[i:])
	out[i] = to
	g.edges[from] = out
}

// Remove deletes a node and all edges pointing to it.
func (g *Graph) Remove(id string) {
	delete(g.edges, id)
	for n, out := range g.edges {
		if i := sort.SearchStrings(out, id); i < len(out) && out[i] == id {
			g.edges[n] = append(out[:i], out[i+1:]...)
		}
	}
}

// Has reports whether id is a node of the graph.
func (g *Graph) Has(id string) bool {
	_, ok := g.edges[id]
	return ok
}

// Out returns the sorted out-edge targets of id, or nil if id is absent.
// The returned slice is shared with the graph and must not be mutated.
func (g *Graph) Out(id string) []string {
	return g.edges[id]
}

// Size returns the number of nodes.
func (g *Graph) Size() int {
	return len(g.edges)
}

// OutDegree returns the number of out-edges of id.
func (g *Graph) OutDegree(id string) int {
	return len(g.edges[id])
}
