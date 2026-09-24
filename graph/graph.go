// Package graph 维护带重数的有向边集，以及正向与反向邻接。
// 边存在当且仅当重数 > 0；节点在第一次出现在边上时隐式存在。
package graph

// Graph 是带重数的有向图。不保证并发安全，由上层加锁。
type Graph struct {
	mult map[[2]string]int          // (u,v) -> 重数，只存重数 > 0 的边
	out  map[string]map[string]bool // u -> 出边终点集（仅存在的边）
	in   map[string]map[string]bool // v -> 入边起点集（仅存在的边）
}

// New 返回空图。
func New() *Graph {
	return &Graph{
		mult: make(map[[2]string]int),
		out:  make(map[string]map[string]bool),
		in:   make(map[string]map[string]bool),
	}
}

// Has 报告边 (u,v) 是否存在（重数 > 0）。
func (g *Graph) Has(u, v string) bool { return g.mult[[2]string{u, v}] > 0 }

// Edges 返回当前存在的不同边的个数。
func (g *Graph) Edges() int { return len(g.mult) }

// Add 使 (u,v) 重数加 1，返回该边是否由不存在变为存在。
func (g *Graph) Add(u, v string) bool {
	e := [2]string{u, v}
	g.mult[e]++
	if g.mult[e] > 1 {
		return false
	}
	if g.out[u] == nil {
		g.out[u] = make(map[string]bool)
	}
	g.out[u][v] = true
	if g.in[v] == nil {
		g.in[v] = make(map[string]bool)
	}
	g.in[v][u] = true
	return true
}

// Remove 使 (u,v) 重数减 1，返回该边是否因此消失。调用前须保证边存在。
func (g *Graph) Remove(u, v string) bool {
	e := [2]string{u, v}
	g.mult[e]--
	if g.mult[e] > 0 {
		return false
	}
	delete(g.mult, e)
	delete(g.out[u], v)
	if len(g.out[u]) == 0 {
		delete(g.out, u)
	}
	delete(g.in[v], u)
	if len(g.in[v]) == 0 {
		delete(g.in, v)
	}
	return true
}

// Out 返回 u 的出边终点（仅存在的边）。
func (g *Graph) Out(u string) []string {
	var vs []string
	for v := range g.out[u] {
		vs = append(vs, v)
	}
	return vs
}

// In 返回 v 的入边起点（仅存在的边）。
func (g *Graph) In(v string) []string {
	var us []string
	for u := range g.in[v] {
		us = append(us, u)
	}
	return us
}

// Nodes 返回所有出现过且当前仍关联至少一条存在边的节点。
func (g *Graph) Nodes() []string {
	seen := make(map[string]bool)
	var ns []string
	for u, vs := range g.out {
		if !seen[u] {
			seen[u] = true
			ns = append(ns, u)
		}
		for v := range vs {
			if !seen[v] {
				seen[v] = true
				ns = append(ns, v)
			}
		}
	}
	return ns
}
