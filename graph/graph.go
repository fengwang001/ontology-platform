// Package graph implements a directed graph whose nodes are named objects and
// whose edges are directed Links between objects.
package graph

import "errors"

var (
	ErrNodeExists    = errors.New("graph: node already exists")
	ErrNodeNotFound  = errors.New("graph: node not found")
	ErrSelfLoop      = errors.New("graph: self loops are not allowed")
	ErrDuplicateEdge = errors.New("graph: duplicate edge")
)

// Graph is a directed graph with insertion-ordered adjacency lists.
type Graph struct {
	nodes map[string]struct{}
	out   map[string][]string
	in    map[string][]string

	// edgeExaminations counts edges scanned by the last HasCycle call.
	// It backs the O(V+E) invariant and is reset on every call.
	edgeExaminations int
}

func New() *Graph {
	return &Graph{
		nodes: make(map[string]struct{}),
		out:   make(map[string][]string),
		in:    make(map[string][]string),
	}
}

func (g *Graph) AddNode(name string) error {
	if _, ok := g.nodes[name]; ok {
		return ErrNodeExists
	}
	g.nodes[name] = struct{}{}
	g.out[name] = nil
	g.in[name] = nil
	return nil
}

func (g *Graph) AddEdge(from, to string) error {
	if _, ok := g.nodes[from]; !ok {
		return ErrNodeNotFound
	}
	if _, ok := g.nodes[to]; !ok {
		return ErrNodeNotFound
	}
	if from == to {
		return ErrSelfLoop
	}
	for _, n := range g.out[from] {
		if n == to {
			return ErrDuplicateEdge
		}
	}
	g.out[from] = append(g.out[from], to)
	g.in[to] = append(g.in[to], from)
	return nil
}

func (g *Graph) HasNode(name string) bool {
	_, ok := g.nodes[name]
	return ok
}

func (g *Graph) NodeCount() int { return len(g.nodes) }

func (g *Graph) EdgeCount() int {
	total := 0
	for _, nexts := range g.out {
		total += len(nexts)
	}
	return total
}

// Out returns the outgoing neighbors of name in insertion order.
func (g *Graph) Out(name string) []string { return append([]string(nil), g.out[name]...) }

// In returns the incoming neighbors of name in insertion order.
func (g *Graph) In(name string) []string { return append([]string(nil), g.in[name]...) }

// EdgeExaminations reports how many adjacency entries the last HasCycle
// scanned. Every edge is examined at most once, hence the value is <= E.
func (g *Graph) EdgeExaminations() int { return g.edgeExaminations }

// HasCycle reports whether the graph contains a directed cycle, using the
// three-color DFS: an edge back to a gray (on-stack) node is a cycle, while an
// edge to a black (fully finished) node is a diamond convergence and is not.
func (g *Graph) HasCycle() bool {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[string]int, len(g.nodes))
	for name := range g.nodes {
		color[name] = white
	}
	g.edgeExaminations = 0

	var visit func(string) bool
	visit = func(from string) bool {
		color[from] = gray
		for _, to := range g.out[from] {
			g.edgeExaminations++
			switch color[to] {
			case gray:
				return true
			case white:
				if visit(to) {
					return true
				}
			}
			// black: already fully explored on another path, not a cycle.
		}
		color[from] = black
		return false
	}

	for name := range g.nodes {
		if color[name] == white && visit(name) {
			return true
		}
	}
	return false
}
