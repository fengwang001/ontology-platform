// Package graph 提供不可变的邻接表有向图。
// 节点为字符串 ID；每个节点的出边按目标 ID 字典序排列并去重。
package graph

import "sort"

// Graph 是构造后不可变的有向图。
type Graph struct {
	nodes map[string]struct{}
	adj   map[string][]string
}

// Builder 构造图。构造完成后调用 Build 得到不可变 Graph。
type Builder struct {
	edges map[string]map[string]struct{}
	nodes map[string]struct{}
}

// NewBuilder 创建空构造器。
func NewBuilder() *Builder {
	return &Builder{
		edges: map[string]map[string]struct{}{},
		nodes: map[string]struct{}{},
	}
}

// AddNode 注册一个可能没有出边的孤立节点。
func (b *Builder) AddNode(id string) {
	b.nodes[id] = struct{}{}
}

// AddEdge 加入一条从 from 指向 to 的有向边；重复边只保留一次。
func (b *Builder) AddEdge(from, to string) {
	b.nodes[from] = struct{}{}
	b.nodes[to] = struct{}{}
	set, ok := b.edges[from]
	if !ok {
		set = map[string]struct{}{}
		b.edges[from] = set
	}
	set[to] = struct{}{}
}

// Build 冻结构造器并产出不可变图；出边按目标 ID 字典序排列。
func (b *Builder) Build() *Graph {
	g := &Graph{
		nodes: make(map[string]struct{}, len(b.nodes)),
		adj:   make(map[string][]string, len(b.nodes)),
	}
	for id := range b.nodes {
		g.nodes[id] = struct{}{}
	}
	for from, set := range b.edges {
		targets := make([]string, 0, len(set))
		for to := range set {
			targets = append(targets, to)
		}
		sort.Strings(targets)
		g.adj[from] = targets
	}
	return g
}

// Has 报告节点是否存在。
func (g *Graph) Has(id string) bool {
	_, ok := g.nodes[id]
	return ok
}

// Nodes 按字典序返回全部节点 ID。返回切片仅供只读使用。
func (g *Graph) Nodes() []string {
	if len(g.nodes) == 0 {
		return nil
	}
	ids := make([]string, 0, len(g.nodes))
	for id := range g.nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Neighbors 返回节点的有序出边目标；不存在的节点返回 nil。
// 返回切片仅供只读使用。
func (g *Graph) Neighbors(id string) []string {
	return g.adj[id]
}

// OutDegree 返回节点的出度；不存在的节点返回 0。
func (g *Graph) OutDegree(id string) int {
	return len(g.adj[id])
}
