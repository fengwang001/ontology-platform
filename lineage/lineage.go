// Package lineage 维护列间派生边与无环性质，并提供各 kind 的级别传递函数。
package lineage

import "sort"

// Kind 是派生边的变换类型。
type Kind int

// 四种派生关系。
const (
	Copy Kind = iota
	Mask
	Hash
	Agg
)

// ValidKind 报告 kind 是否合法。
func ValidKind(k Kind) bool { return k >= Copy && k <= Agg }

// Edge 是一条派生边。
type Edge struct {
	Src  string
	Dst  string
	Kind Kind
}

// Apply 返回沿边传递后的级别：
// Copy=x, Mask=max(x-1,0), Hash=max(x-2,0), Agg=min(x,2)。
func (k Kind) Apply(x int) int {
	switch k {
	case Mask:
		if x > 1 {
			return x - 1
		}
		return 0
	case Hash:
		if x > 2 {
			return x - 2
		}
		return 0
	case Agg:
		if x > 2 {
			return 2
		}
		return x
	default:
		return x
	}
}

// Graph 是有向无环图，非并发安全，由上层加锁。
type Graph struct {
	edges map[string]map[string]Kind // src -> dst -> kind
	in    map[string]map[string]Kind // dst -> src -> kind
}

// NewGraph 创建空图。
func NewGraph() *Graph {
	return &Graph{
		edges: make(map[string]map[string]Kind),
		in:    make(map[string]map[string]Kind),
	}
}

// HasEdge 报告边是否存在。
func (g *Graph) HasEdge(src, dst string) bool {
	_, ok := g.edges[src][dst]
	return ok
}

// KindOf 返回边的 kind，不存在时 ok 为 false。
func (g *Graph) KindOf(src, dst string) (Kind, bool) {
	k, ok := g.edges[src][dst]
	return k, ok
}

// InDegree 返回列的入边数。
func (g *Graph) InDegree(col string) int { return len(g.in[col]) }

// OutDegree 返回列的出边数。
func (g *Graph) OutDegree(col string) int { return len(g.edges[col]) }

// InEdges 返回列的全部入边（不保证顺序）。
func (g *Graph) InEdges(col string) []Edge {
	ins := g.in[col]
	srcs := make([]string, 0, len(ins))
	for src := range ins {
		srcs = append(srcs, src)
	}
	sort.Strings(srcs)
	edges := make([]Edge, 0, len(srcs))
	for _, src := range srcs {
		edges = append(edges, Edge{Src: src, Dst: col, Kind: ins[src]})
	}
	return edges
}

// OutNeighbors 返回列的直接下游列名（不保证顺序）。
func (g *Graph) OutNeighbors(col string) []string {
	dsts := make([]string, 0, len(g.edges[col]))
	for dst := range g.edges[col] {
		dsts = append(dsts, dst)
	}
	sort.Strings(dsts)
	return dsts
}

// Reaches 报告沿边从 from 是否能到达 to（不含长度 0）。
func (g *Graph) Reaches(from, to string) bool {
	seen := map[string]bool{from: true}
	frontier := []string{from}
	for len(frontier) > 0 {
		cur := frontier[len(frontier)-1]
		frontier = frontier[:len(frontier)-1]
		for dst := range g.edges[cur] {
			if dst == to {
				return true
			}
			if !seen[dst] {
				seen[dst] = true
				frontier = append(frontier, dst)
			}
		}
	}
	return false
}

// Add 加入一条边；已存在同一 (src,dst) 边时返回 false。
func (g *Graph) Add(src, dst string, kind Kind) bool {
	if g.HasEdge(src, dst) {
		return false
	}
	out := g.edges[src]
	if out == nil {
		out = make(map[string]Kind)
		g.edges[src] = out
	}
	out[dst] = kind
	in := g.in[dst]
	if in == nil {
		in = make(map[string]Kind)
		g.in[dst] = in
	}
	in[src] = kind
	return true
}

// Remove 删除一条边；不存在时返回 false。
func (g *Graph) Remove(src, dst string) bool {
	if _, ok := g.edges[src][dst]; !ok {
		return false
	}
	delete(g.edges[src], dst)
	delete(g.in[dst], src)
	return true
}
