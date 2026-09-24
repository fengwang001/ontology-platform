// Package graph 提供只读的字符串节点邻接表。
//
// 邻接关系在构建时排序并去重，因此出边的考察顺序只由目标 ID 的字典序决定，
// 不依赖任何 map 迭代顺序。图构建后不可变，遍历期间的“改动”通过显式的
// 删除操作产生一张新的逻辑快照（共享底层结构），用于续传一致性自检。
package graph

import "sort"

// Graph 是构建后只读的有向图。
type Graph struct {
	nodes map[string]struct{}
	adj   map[string][]string
}

// Builder 累积边并产出不可变 Graph。
type Builder struct {
	nodes map[string]struct{}
	edges map[string]map[string]struct{}
}

// NewBuilder 创建空构建器。
func NewBuilder() *Builder {
	return &Builder{
		nodes: map[string]struct{}{},
		edges: map[string]map[string]struct{}{},
	}
}

// Node 声明一个节点（孤立节点也需声明，Has 才会为真）。
func (b *Builder) Node(id string) {
	b.nodes[id] = struct{}{}
}

// Edge 声明一条有向边 from->to，端点会被自动加入节点集合。重复边安全。
func (b *Builder) Edge(from, to string) {
	b.Node(from)
	b.Node(to)
	set := b.edges[from]
	if set == nil {
		set = map[string]struct{}{}
		b.edges[from] = set
	}
	set[to] = struct{}{}
}

// Build 冻结构建器，产出出边已按目标 ID 字典序排序去重的图。
func (b *Builder) Build() *Graph {
	adj := make(map[string][]string, len(b.edges))
	for from, set := range b.edges {
		out := make([]string, 0, len(set))
		for to := range set {
			out = append(out, to)
		}
		sort.Strings(out)
		adj[from] = out
	}
	nodes := make(map[string]struct{}, len(b.nodes))
	for id := range b.nodes {
		nodes[id] = struct{}{}
	}
	return &Graph{nodes: nodes, adj: adj}
}

// Has 报告节点是否存在。
func (g *Graph) Has(id string) bool {
	_, ok := g.nodes[id]
	return ok
}

// Nodes 返回全部节点 ID，按字典序排列（仅供测试/演示，不参与遍历顺序）。
func (g *Graph) Nodes() []string {
	out := make([]string, 0, len(g.nodes))
	for id := range g.nodes {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Out 返回节点的出边目标（字典序、已去重）。未知节点返回 nil。
func (g *Graph) Out(id string) []string {
	return g.adj[id]
}

// Reachable 从 start 做去重遍历，返回可达节点数；start 不存在时返回 0。
func (g *Graph) Reachable(start string) int {
	if !g.Has(start) {
		return 0
	}
	seen := map[string]struct{}{start: {}}
	stack := []string{start}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, next := range g.Out(cur) {
			if _, ok := seen[next]; !ok {
				seen[next] = struct{}{}
				stack = append(stack, next)
			}
		}
	}
	return len(seen)
}

// Without 返回一张逻辑新图：在原快照上删除节点 id（及其所有出入边）。
// 用于模拟“两段续传之间图被改动”，不修改接收者。
func (g *Graph) Without(id string) *Graph {
	nodes := make(map[string]struct{}, len(g.nodes))
	for n := range g.nodes {
		if n != id {
			nodes[n] = struct{}{}
		}
	}
	adj := make(map[string][]string, len(g.adj))
	for from, out := range g.adj {
		if from == id {
			continue
		}
		kept := make([]string, 0, len(out))
		for _, to := range out {
			if to != id {
				kept = append(kept, to)
			}
		}
		adj[from] = kept
	}
	return &Graph{nodes: nodes, adj: adj}
}
