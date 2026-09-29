// Package graphmatch implements subgraph pattern matching over an object
// graph. A pattern consists of node variables with type/property constraints
// and typed edge constraints between variables. Matching uses backtracking
// search with early pruning, returns isomorphic matches only once, and binds
// the same variable to the same object consistently.
package graphmatch

import (
	"sort"
	"sync"
)

// Object is a node in the object graph.
type Object struct {
	ID    string
	Type  string
	Props map[string]string
}

// Edge is a typed directed edge between two objects.
type Edge struct {
	From string
	To   string
	Type string
}

// Graph is a concurrency-safe object graph. Iteration order is canonical
// (sorted), so the edge insertion order never affects match results.
type Graph struct {
	mu      sync.RWMutex
	objects map[string]Object
	edges   map[Edge]struct{}
	out     map[string][]Edge
	in      map[string][]Edge
}

// NewGraph returns an empty graph.
func NewGraph() *Graph {
	return &Graph{
		objects: make(map[string]Object),
		edges:   make(map[Edge]struct{}),
		out:     make(map[string][]Edge),
		in:      make(map[string][]Edge),
	}
}

// AddObject inserts or replaces an object by ID.
func (g *Graph) AddObject(o Object) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.objects[o.ID] = o
}

// AddEdge inserts an edge. Duplicate edges are idempotent.
func (g *Graph) AddEdge(e Edge) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.edges[e]; ok {
		return
	}
	g.edges[e] = struct{}{}
	g.out[e.From] = append(g.out[e.From], e)
	g.in[e.To] = append(g.in[e.To], e)
}

// snapshot is an immutable, canonically ordered view of the graph.
type snapshot struct {
	objects []Object
	byID    map[string]Object
	out     map[string][]Edge
	in      map[string][]Edge
}

// snapshot takes a consistent, canonically ordered read view of the graph.
func (g *Graph) snapshot() *snapshot {
	g.mu.RLock()
	defer g.mu.RUnlock()
	s := &snapshot{
		byID: make(map[string]Object, len(g.objects)),
		out:  make(map[string][]Edge, len(g.out)),
		in:   make(map[string][]Edge, len(g.in)),
	}
	for id, o := range g.objects {
		s.byID[id] = o
		s.objects = append(s.objects, o)
	}
	sort.Slice(s.objects, func(i, j int) bool { return s.objects[i].ID < s.objects[j].ID })
	for id, es := range g.out {
		cp := append([]Edge(nil), es...)
		sortEdges(cp)
		s.out[id] = cp
	}
	for id, es := range g.in {
		cp := append([]Edge(nil), es...)
		sortEdges(cp)
		s.in[id] = cp
	}
	return s
}

func sortEdges(es []Edge) {
	sort.Slice(es, func(i, j int) bool {
		if es[i].From != es[j].From {
			return es[i].From < es[j].From
		}
		if es[i].To != es[j].To {
			return es[i].To < es[j].To
		}
		return es[i].Type < es[j].Type
	})
}
