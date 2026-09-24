// Package graph 提供邻接表图：节点为字符串 ID，出边按目标 ID 字典序排列。
//
// 图在遍历期间必须只读；AddEdge/AddNode/RemoveNode 与读取并发是不安全的。
// 包内不持有任何遍历状态，读取是纯函数。
package graph

import (
	"slices"
	"sort"
)

// Graph 是邻接表。adj 的每个值切片按目标 ID 字典序升序，允许重复边
// （重复边由遍历侧的已发现集合去重，此处保留以考察该路径）。
type Graph struct {
	adj map[string][]string
}

// New 返回空图。
func New() *Graph {
	return &Graph{adj: make(map[string][]string)}
}

// AddNode 注册一个节点（可无出边）。重复注册是幂等的。
func (g *Graph) AddNode(id string) {
	if _, ok := g.adj[id]; !ok {
		g.adj[id] = nil
	}
}

// AddEdge 添加有向边 from->to，两端节点自动注册。
// 出边按目标 ID 字典序插入，重复边保留。
func (g *Graph) AddEdge(from, to string) {
	g.AddNode(to)
	outs := g.adj[from]
	pos, _ := slices.BinarySearch(outs, to)
	outs = slices.Insert(outs, pos, to)
	g.adj[from] = outs
}

// Has 报告节点是否存在。
func (g *Graph) Has(id string) bool {
	_, ok := g.adj[id]
	return ok
}

// Edges 返回节点的出边目标（字典序，含重复），第二个返回值报告节点是否存在。
// 返回的切片为内部状态，调用方不得修改。
func (g *Graph) Edges(id string) ([]string, bool) {
	outs, ok := g.adj[id]
	return outs, ok
}

// Degree 返回节点的出度（含重复边）。节点不存在时返回 0。
func (g *Graph) Degree(id string) int {
	return len(g.adj[id])
}

// Size 返回节点数。
func (g *Graph) Size() int {
	return len(g.adj)
}

// Nodes 按字典序返回全部节点 ID。
func (g *Graph) Nodes() []string {
	ids := make([]string, 0, len(g.adj))
	for id := range g.adj {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// RemoveNode 删除节点及其出边；其他节点指向它的边成为悬挂边，
// 遍历到该节点时由 walk 包报告指名错误。
func (g *Graph) RemoveNode(id string) {
	delete(g.adj, id)
}
