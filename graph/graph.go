// Package graph provides an in-memory adjacency-list graph whose nodes are
// string IDs and whose out-edges are kept sorted by target ID.
package graph

import "sort"

// Graph is an adjacency-list graph. The zero value is not usable; use New.
// A Graph is safe for concurrent reads, but reads must not race with writes.
type Graph struct {
	out map[string][]string
}

// New returns an empty graph.
func New() *Graph {
	return &Graph{out: make(map[string][]string)}
}

// AddNode ensures id exists in the graph.
func (g *Graph) AddNode(id string) {
	if _, ok := g.out[id]; !ok {
		g.out[id] = nil
	}
}

// AddEdge adds a directed edge from -> to, creating endpoints as needed.
// Duplicate and self-loop edges are stored as given; traversal dedupes visits.
// Out-edges are kept sorted by target ID so expansion order is deterministic.
func (g *Graph) AddEdge(from, to string) {
	g.AddNode(from)
	g.AddNode(to)
	g.out[from] = append(g.out[from], to)
	sort.Strings(g.out[from])
}

// RemoveNode deletes id and its out-edges. In-edges from other nodes remain.
func (g *Graph) RemoveNode(id string) {
	delete(g.out, id)
}

// Has reports whether id is a node of the graph.
func (g *Graph) Has(id string) bool {
	_, ok := g.out[id]
	return ok
}

// Out returns the out-edges of id sorted by target ID, or nil if id is
// absent. The returned slice is shared; callers must not mutate it.
func (g *Graph) Out(id string) []string {
	return g.out[id]
}

// Size returns the number of nodes.
func (g *Graph) Size() int {
	return len(g.out)
}
