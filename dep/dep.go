// Package dep defines the fixed dependency graph between base tables and
// derived views: direct edges, the reverse (dependents) index, and the
// transitive base-table closure used for staleness tests.
package dep

import "sort"

// Kind classifies a named node.
type Kind int

const (
	Base Kind = iota + 1
	View
)

// Node is a base table or a derived view. Deps lists direct dependency names.
type Node struct {
	Name string
	Kind Kind
	Deps []string
}

// Graph is an immutable directed dependency graph.
type Graph struct {
	nodes      map[string]Node
	direct     map[string][]string
	dependents map[string][]string
	bases      map[string][]string
}

// New builds a graph from the given nodes.
func New(nodes ...Node) *Graph {
	g := &Graph{
		nodes:      map[string]Node{},
		direct:     map[string][]string{},
		dependents: map[string][]string{},
		bases:      map[string][]string{},
	}
	for _, n := range nodes {
		g.nodes[n.Name] = n
		g.direct[n.Name] = append([]string(nil), n.Deps...)
	}
	for _, n := range nodes {
		for _, d := range n.Deps {
			g.dependents[d] = append(g.dependents[d], n.Name)
		}
	}
	for name := range g.nodes {
		g.bases[name] = g.computeBases(name)
	}
	return g
}

// Exists reports whether name is a known node.
func (g *Graph) Exists(name string) bool {
	_, ok := g.nodes[name]
	return ok
}

// KindOf returns the kind of a node and whether it exists.
func (g *Graph) KindOf(name string) (Kind, bool) {
	n, ok := g.nodes[name]
	if !ok {
		return 0, false
	}
	return n.Kind, true
}

// Direct returns the direct dependency names of a node.
func (g *Graph) Direct(name string) []string {
	out := append([]string(nil), g.direct[name]...)
	return out
}

// Dependents returns the nodes that directly depend on name (reverse edges).
func (g *Graph) Dependents(name string) []string {
	return append([]string(nil), g.dependents[name]...)
}

// TransitiveBases returns every base table reachable from name along dep edges.
func (g *Graph) TransitiveBases(name string) []string {
	return append([]string(nil), g.bases[name]...)
}

func (g *Graph) computeBases(name string) []string {
	seen := map[string]bool{}
	var walk func(string)
	walk = func(cur string) {
		n, ok := g.nodes[cur]
		if !ok {
			return
		}
		if n.Kind == Base {
			seen[cur] = true
			return
		}
		for _, d := range n.Deps {
			walk(d)
		}
	}
	walk(name)
	out := make([]string, 0, len(seen))
	for b := range seen {
		out = append(out, b)
	}
	sort.Strings(out)
	return out
}
