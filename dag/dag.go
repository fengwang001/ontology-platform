// Package dag validates job dependency graphs and answers
// downstream-closure queries.
package dag

import (
	"errors"
	"fmt"
	"math/bits"
)

// Validation error classes, distinguishable with errors.Is.
var (
	ErrDuplicateName = errors.New("dag: duplicate job name")
	ErrUnknownNeed   = errors.New("dag: unknown dependency")
	ErrCycle         = errors.New("dag: dependency cycle")
)

// Graph is a validated acyclic dependency graph. Nodes are identified by
// their index in the Names slice.
type Graph struct {
	names   []string
	index   map[string]int
	needs   [][]int
	downs   [][]int
	closure [][]uint64 // transitive downstream bitsets, self excluded
}

// Build validates names/needs and returns the graph. Error classes are
// reported in order: duplicate names, then unknown needs, then cycles;
// only the first class encountered is returned.
func Build(names []string, needs [][]string) (*Graph, error) {
	g := &Graph{
		names: append([]string(nil), names...),
		index: make(map[string]int, len(names)),
	}
	for i, n := range names {
		if _, dup := g.index[n]; dup {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateName, n)
		}
		g.index[n] = i
	}
	g.needs = make([][]int, len(names))
	g.downs = make([][]int, len(names))
	for i, ns := range needs {
		seen := make(map[int]bool, len(ns))
		for _, n := range ns {
			j, ok := g.index[n]
			if !ok {
				return nil, fmt.Errorf("%w: %q needed by %q", ErrUnknownNeed, n, names[i])
			}
			if seen[j] {
				continue
			}
			seen[j] = true
			g.needs[i] = append(g.needs[i], j)
			g.downs[j] = append(g.downs[j], i)
		}
	}
	order, err := g.topoOrder()
	if err != nil {
		return nil, err
	}
	g.buildClosure(order)
	return g, nil
}

// topoOrder returns a needs-first ordering (Kahn), or ErrCycle.
func (g *Graph) topoOrder() ([]int, error) {
	n := len(g.names)
	indeg := make([]int, n)
	for i := range g.needs {
		indeg[i] = len(g.needs[i])
	}
	order := make([]int, 0, n)
	queue := make([]int, 0, n)
	for i := 0; i < n; i++ {
		if indeg[i] == 0 {
			queue = append(queue, i)
		}
	}
	for len(queue) > 0 {
		i := queue[0]
		queue = queue[1:]
		order = append(order, i)
		for _, d := range g.downs[i] {
			indeg[d]--
			if indeg[d] == 0 {
				queue = append(queue, d)
			}
		}
	}
	if len(order) != n {
		for i := 0; i < n; i++ {
			if indeg[i] > 0 {
				return nil, fmt.Errorf("%w: involves %q", ErrCycle, g.names[i])
			}
		}
		return nil, ErrCycle
	}
	return order, nil
}

// buildClosure computes transitive downstream bitsets in reverse
// topological order.
func (g *Graph) buildClosure(order []int) {
	n := len(g.names)
	w := (n + 63) / 64
	g.closure = make([][]uint64, n)
	for i := range g.closure {
		g.closure[i] = make([]uint64, w)
	}
	for k := n - 1; k >= 0; k-- {
		i := order[k]
		for _, d := range g.downs[i] {
			g.closure[i][d/64] |= 1 << (uint(d) % 64)
			for word := 0; word < w; word++ {
				g.closure[i][word] |= g.closure[d][word]
			}
		}
	}
}

// Len returns the number of nodes.
func (g *Graph) Len() int { return len(g.names) }

// Name returns the name of node i.
func (g *Graph) Name(i int) string { return g.names[i] }

// Index returns the index of the named node.
func (g *Graph) Index(name string) (int, bool) {
	i, ok := g.index[name]
	return i, ok
}

// Needs returns the direct upstream indices of node i.
func (g *Graph) Needs(i int) []int { return g.needs[i] }

// Downstream returns the direct downstream indices of node i.
func (g *Graph) Downstream(i int) []int { return g.downs[i] }

// Closure returns the transitive downstream indices of node i
// (self excluded), in ascending index order.
func (g *Graph) Closure(i int) []int {
	var out []int
	for word, w := range g.closure[i] {
		for b := w; b != 0; b &= b - 1 {
			out = append(out, word*64+bits.TrailingZeros64(b))
		}
	}
	return out
}
