// Package model describes and validates process definition graphs.
package model

import "errors"

// NodeType enumerates the eight kinds of flow nodes.
type NodeType int

const (
	Start NodeType = iota
	Task
	AndSplit
	XorSplit
	OrSplit
	AndJoin
	OrJoin
	End
)

// Validation errors, reported in the order ErrStructure, ErrCycle, ErrReach.
var (
	ErrStructure = errors.New("model: structural violation")
	ErrCycle     = errors.New("model: graph contains a cycle")
	ErrReach     = errors.New("model: node unreachable from Start or cannot reach any End")
)

// MaxNodes bounds the node count so bitset reachability fits in uint64.
const MaxNodes = 64

// Edge is a directed edge From -> To using 1-based node ids.
type Edge struct {
	From, To int
}

// Graph is a process definition. Kind is 1-based (Kind[1..N]); Edges keep
// insertion order, which defines the out-edge numbering 0,1,... per node.
type Graph struct {
	N     int
	Kind  []NodeType
	Edges []Edge
}

// Out returns the out-edges of u in insertion (numbering) order.
func (g *Graph) Out(u int) []Edge {
	var out []Edge
	for _, e := range g.Edges {
		if e.From == u {
			out = append(out, e)
		}
	}
	return out
}

// In returns the in-edges of v in insertion order.
func (g *Graph) In(v int) []Edge {
	var in []Edge
	for _, e := range g.Edges {
		if e.To == v {
			in = append(in, e)
		}
	}
	return in
}

// Clone deep-copies the graph so callers can retain immutable snapshots.
func (g *Graph) Clone() *Graph {
	c := &Graph{N: g.N, Kind: append([]NodeType(nil), g.Kind...), Edges: append([]Edge(nil), g.Edges...)}
	return c
}

// IsJoin and IsChoiceSplit classify node types for engine and repo.
func IsJoin(t NodeType) bool { return t == AndJoin || t == OrJoin }

func IsChoiceSplit(t NodeType) bool { return t == XorSplit || t == OrSplit }

// Validate checks structure, acyclicity and reachability, in that order.
func Validate(g *Graph) error {
	if g == nil || g.N < 1 || g.N > MaxNodes || len(g.Kind) < g.N+1 {
		return ErrStructure
	}
	inDeg := make([]int, g.N+1)
	outDeg := make([]int, g.N+1)
	seen := make(map[Edge]bool, len(g.Edges))
	for _, e := range g.Edges {
		if e.From < 1 || e.From > g.N || e.To < 1 || e.To > g.N || e.From == e.To || seen[e] {
			return ErrStructure
		}
		seen[e] = true
		outDeg[e.From]++
		inDeg[e.To]++
	}
	starts := 0
	for v := 1; v <= g.N; v++ {
		switch g.Kind[v] {
		case Start:
			starts++
			if inDeg[v] != 0 || outDeg[v] != 1 {
				return ErrStructure
			}
		case Task:
			if inDeg[v] < 1 || outDeg[v] != 1 {
				return ErrStructure
			}
		case AndSplit, XorSplit, OrSplit:
			if inDeg[v] != 1 || outDeg[v] < 2 {
				return ErrStructure
			}
		case AndJoin, OrJoin:
			if inDeg[v] < 2 || outDeg[v] != 1 {
				return ErrStructure
			}
		case End:
			if inDeg[v] < 1 || outDeg[v] != 0 {
				return ErrStructure
			}
		default:
			return ErrStructure
		}
	}
	if starts != 1 {
		return ErrStructure
	}
	if hasCycle(g) {
		return ErrCycle
	}
	return checkReach(g)
}

func hasCycle(g *Graph) bool {
	const (
		white, gray, black = 0, 1, 2
	)
	color := make([]int, g.N+1)
	var visit func(u int) bool
	visit = func(u int) bool {
		color[u] = gray
		for _, e := range g.Out(u) {
			if color[e.To] == gray || (color[e.To] == white && visit(e.To)) {
				return true
			}
		}
		color[u] = black
		return false
	}
	for v := 1; v <= g.N; v++ {
		if color[v] == white && visit(v) {
			return true
		}
	}
	return false
}

func checkReach(g *Graph) error {
	start := 0
	var ends []int
	for v := 1; v <= g.N; v++ {
		if g.Kind[v] == Start {
			start = v
		}
		if g.Kind[v] == End {
			ends = append(ends, v)
		}
	}
	fromStart := bfs(g.N, []int{start}, g.Out, func(e Edge) int { return e.To })
	toEnd := bfs(g.N, ends, g.In, func(e Edge) int { return e.From })
	for v := 1; v <= g.N; v++ {
		if !fromStart[v] || !toEnd[v] {
			return ErrReach
		}
	}
	return nil
}

func bfs(n int, queue []int, edges func(int) []Edge, peer func(Edge) int) []bool {
	seen := make([]bool, n+1)
	for _, s := range queue {
		seen[s] = true
	}
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		for _, e := range edges(u) {
			w := peer(e)
			if !seen[w] {
				seen[w] = true
				queue = append(queue, w)
			}
		}
	}
	return seen
}
