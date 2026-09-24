// Package graph 提供只读邻接表：节点为字符串 ID，出边按目标 ID 字典序、去重。
package graph

import "sort"

// Graph 是只读的有向邻接表。零值不可用，必须通过 Build 构造。
type Graph struct {
	nodes []string       // 全部节点，字典序
	index map[string]int // 节点 -> nodes 下标
	edges [][]string     // edges[i] 为 nodes[i] 的出边目标，字典序、去重
	out   []int          // out[i] len(edges[i])，空邻接时避免解引用
}

// Builder 累积边，Build 时排序去重，之后图不可变。
type Builder struct {
	seen map[string]map[string]bool
}

// NewBuilder 创建空构造器。
func NewBuilder() *Builder {
	return &Builder{seen: map[string]map[string]bool{}}
}

// AddNode 注册一个没有出边的孤立节点。
func (b *Builder) AddNode(id string) {
	if b.seen[id] == nil {
		b.seen[id] = map[string]bool{}
	}
}

// AddEdge 注册一条有向边（含自环、重复边）。
func (b *Builder) AddEdge(from, to string) {
	if b.seen[from] == nil {
		b.seen[from] = map[string]bool{}
	}
	if b.seen[to] == nil {
		b.seen[to] = map[string]bool{}
	}
	b.seen[from][to] = true
}

// Build 产出只读图：收集所有出现过的 ID，出边排序去重。
func (b *Builder) Build() *Graph {
	ids := make([]string, 0, len(b.seen))
	for id := range b.seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	g := &Graph{nodes: ids, index: make(map[string]int, len(ids)), edges: make([][]string, len(ids))}
	for i, id := range ids {
		g.index[id] = i
		targets := make([]string, 0, len(b.seen[id]))
		for t := range b.seen[id] {
			targets = append(targets, t)
		}
		sort.Strings(targets)
		g.edges[i] = targets
	}
	return g
}

// Has 报告节点是否存在。
func (g *Graph) Has(id string) bool {
	_, ok := g.index[id]
	return ok
}

// Nodes 返回全部节点 ID（字典序）。
func (g *Graph) Nodes() []string {
	out := make([]string, len(g.nodes))
	copy(out, g.nodes)
	return out
}

// Targets 返回节点的出边目标（字典序、去重）；节点不存在返回 nil。
func (g *Graph) Targets(id string) []string {
	i, ok := g.index[id]
	if !ok {
		return nil
	}
	out := make([]string, len(g.edges[i]))
	copy(out, g.edges[i])
	return out
}

// Delete 返回一个删除指定节点及其关联边后的新图（原图不变），
// 用于模拟「两段续传之间图被改动」。
func (g *Graph) Delete(id string) *Graph {
	b := NewBuilder()
	for _, n := range g.nodes {
		if n == id {
			continue
		}
		b.AddNode(n)
		for _, t := range g.edges[g.index[n]] {
			if t != id {
				b.AddEdge(n, t)
			}
		}
	}
	return b.Build()
}
