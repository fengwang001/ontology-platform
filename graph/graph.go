// Package graph 提供字符串 ID 的有向邻接表。
// 出边按目标 ID 字典序去重存储；自环允许。
package graph

import "sort"

// Graph 是有向图。nodes[id] 为该节点排序去重后的出边目标。
type Graph struct{ nodes map[string][]string }

// New 从邻接描述构造图；边的所有端点都会成为图节点。
func New(edges map[string][]string) *Graph {
	g := &Graph{nodes: make(map[string][]string, len(edges))}
	for u, vs := range edges {
		if _, ok := g.nodes[u]; !ok {
			g.nodes[u] = nil
		}
		for _, v := range vs {
			if _, ok := g.nodes[v]; !ok {
				g.nodes[v] = nil
			}
			g.nodes[u] = append(g.nodes[u], v)
		}
	}
	for u := range g.nodes {
		g.nodes[u] = dedupSort(g.nodes[u])
	}
	return g
}

func dedupSort(in []string) []string {
	if len(in) < 2 {
		return in
	}
	sort.Strings(in)
	out := in[:1]
	for _, v := range in[1:] {
		if v != out[len(out)-1] {
			out = append(out, v)
		}
	}
	return out
}

// Has 报告节点是否存在。
func (g *Graph) Has(id string) bool {
	_, ok := g.nodes[id]
	return ok
}

// Out 返回节点出边目标的字典序拷贝。节点不存在时 ok 为 false。
func (g *Graph) Out(id string) ([]string, bool) {
	out, ok := g.nodes[id]
	if !ok {
		return nil, false
	}
	return append([]string(nil), out...), true
}

// Nodes 返回全部节点 ID（无序，调用方按需排序）。
func (g *Graph) Nodes() []string {
	ids := make([]string, 0, len(g.nodes))
	for id := range g.nodes {
		ids = append(ids, id)
	}
	return ids
}

// Delete 删除节点及其作为源的出边；指向它的入边变为悬空引用，
// 用于「两段续传之间图被改动」的故障注入。
func (g *Graph) Delete(id string) bool {
	if _, ok := g.nodes[id]; !ok {
		return false
	}
	delete(g.nodes, id)
	return true
}

// Star 构造中心 center 连接 leaves 个叶子的星形图。
func Star(center string, leaves int) *Graph {
	edges := make(map[string][]string, leaves+1)
	for i := 0; i < leaves; i++ {
		edges[center] = append(edges[center], leafName(i))
		edges[leafName(i)] = nil
	}
	return New(edges)
}

func leafName(i int) string {
	var b [8]byte
	for p := 7; p >= 0; p-- {
		b[p] = byte('0' + i%10)
		i /= 10
	}
	return "leaf-" + string(b[:])
}
