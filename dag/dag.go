// Package dag holds view nodes, dependency edges, topological sort and
// cycle detection. It must not depend on any other package in this module.
package dag

import (
	"errors"
	"sort"
)

// Sentinel errors (part of the four distinguishable failures).
var (
	ErrEmptyName = errors.New("dag: empty view name")
	ErrExists    = errors.New("dag: view name already exists")
	ErrCycle     = errors.New("dag: dependency cycle detected")
)

// Graph is a directed graph of dependency edges: an edge n -> d means
// view n depends on view d. Edges may point at names that are not yet
// registered (forward references); only registered names are nodes.
type Graph struct {
	nodes      map[string]bool
	deps       map[string][]string // registered node -> its declared deps
	dependents map[string][]string // dep name -> registered nodes depending on it
}

// New returns an empty graph.
func New() *Graph {
	return &Graph{
		nodes:      map[string]bool{},
		deps:       map[string][]string{},
		dependents: map[string][]string{},
	}
}

// Add registers name with the given dependency edges. Forward references
// are allowed; an edge that would close a cycle is rejected and leaves
// the graph unchanged.
func (g *Graph) Add(name string, deps []string) error {
	if name == "" {
		return ErrEmptyName
	}
	if g.nodes[name] {
		return ErrExists
	}
	// A cycle would be closed iff some dep already reaches name.
	for _, d := range deps {
		if g.reaches(d, name) {
			return ErrCycle
		}
	}
	g.nodes[name] = true
	ds := append([]string(nil), deps...)
	g.deps[name] = ds
	for _, d := range ds {
		g.dependents[d] = append(g.dependents[d], name)
	}
	return nil
}

// reaches reports whether target is reachable from start by following
// dependency edges. Unregistered names have no outgoing edges, except
// that edges may terminate at the unregistered target itself.
func (g *Graph) reaches(start, target string) bool {
	seen := map[string]bool{}
	stack := []string{start}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if cur == target {
			return true
		}
		if seen[cur] {
			continue
		}
		seen[cur] = true
		stack = append(stack, g.deps[cur]...)
	}
	return false
}

// Has reports whether name is a registered node.
func (g *Graph) Has(name string) bool { return g.nodes[name] }

// Deps returns the declared dependencies of name (nil if unknown).
func (g *Graph) Deps(name string) []string { return g.deps[name] }

// Downstream returns the transitive closure of registered dependents of
// name (excluding name itself), in deterministic sorted order.
func (g *Graph) Downstream(name string) []string {
	seen := map[string]bool{}
	stack := append([]string(nil), g.dependents[name]...)
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[cur] {
			continue
		}
		seen[cur] = true
		stack = append(stack, g.dependents[cur]...)
	}
	delete(seen, name)
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Topo returns the names in set in dependency-first topological order,
// considering only edges whose both ends are in set. An error means the
// induced subgraph contains a cycle (impossible after a successful Add,
// kept for completeness).
func (g *Graph) Topo(set map[string]bool) ([]string, error) {
	indeg := make(map[string]int, len(set))
	for n := range set {
		for _, d := range g.deps[n] {
			if set[d] {
				indeg[n]++
			}
		}
	}
	var ready []string
	for n := range set {
		if indeg[n] == 0 {
			ready = append(ready, n)
		}
	}
	order := make([]string, 0, len(set))
	for len(ready) > 0 {
		sort.Strings(ready)
		cur := ready[0]
		ready = ready[1:]
		order = append(order, cur)
		for _, dn := range g.dependents[cur] {
			if !set[dn] {
				continue
			}
			indeg[dn]--
			if indeg[dn] == 0 {
				ready = append(ready, dn)
			}
		}
	}
	if len(order) != len(set) {
		return nil, ErrCycle
	}
	return order, nil
}
