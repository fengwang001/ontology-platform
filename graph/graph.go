// Package graph 维护带重数的有向边集，以及正向、反向邻接。
// 边 (u,v) 存在当且仅当其重数 > 0；节点在首次出现在边上时隐式存在。
package graph

import "sort"

// Edge 是一条有向边。
type Edge struct {
	U, V string
}

// Graph 是进程内的带重数有向图，非并发安全（由上层加锁）。
type Graph struct {
	mult map[Edge]int
	out  map[string]map[string]struct{}
	in   map[string]map[string]struct{}
}

// New 创建空图。
func New() *Graph {
	return &Graph{
		mult: map[Edge]int{},
		out:  map[string]map[string]struct{}{},
		in:   map[string]map[string]struct{}{},
	}
}

// Multiplicity 返回边的当前重数（不存在为 0）。
func (g *Graph) Multiplicity(u, v string) int { return g.mult[Edge{u, v}] }

// EdgeCount 返回当前存在的边的条数（与重数无关）。
func (g *Graph) EdgeCount() int { return len(g.mult) }

// Add 使 (u,v) 重数加 1；created 报告边是否从不存在变为存在。
func (g *Graph) Add(u, v string) (created bool) {
	e := Edge{u, v}
	if g.mult[e] == 0 {
		created = true
		if g.out[u] == nil {
			g.out[u] = map[string]struct{}{}
		}
		if g.in[v] == nil {
			g.in[v] = map[string]struct{}{}
		}
		g.out[u][v] = struct{}{}
		g.in[v][u] = struct{}{}
	}
	g.mult[e]++
	return created
}

// Remove 使 (u,v) 重数减 1。调用前边必须存在（Multiplicity > 0）。
// dropped 报告重数是否降为 0（边随之消失）。
func (g *Graph) Remove(u, v string) (dropped bool) {
	e := Edge{u, v}
	m := g.mult[e] // 调用方保证 m >= 1
	if m <= 1 {
		dropped = true
		delete(g.mult, e)
		delete(g.out[u], v)
		delete(g.in[v], u)
	} else {
		g.mult[e] = m - 1
	}
	return dropped
}

// HasEdge 报告边是否存在。
func (g *Graph) HasEdge(u, v string) bool { return g.mult[Edge{u, v}] > 0 }

// HasOut 报告 u 是否有一条正向邻接边到 v。
func (g *Graph) HasOut(u, v string) bool {
	_, ok := g.out[u][v]
	return ok
}

// Out 返回 u 的全部正向邻居（顺序不定）。
func (g *Graph) Out(u string) []string {
	vs := make([]string, 0, len(g.out[u]))
	for v := range g.out[u] {
		vs = append(vs, v)
	}
	return vs
}

// In 返回 v 的全部反向邻居（顺序不定）。
func (g *Graph) In(v string) []string {
	us := make([]string, 0, len(g.in[v]))
	for u := range g.in[v] {
		us = append(us, u)
	}
	return us
}

// Nodes 返回至少出现在一条边（任一端）上的全部节点。
func (g *Graph) Nodes() []string {
	seen := map[string]struct{}{}
	for e := range g.mult {
		seen[e.U] = struct{}{}
		seen[e.V] = struct{}{}
	}
	ns := make([]string, 0, len(seen))
	for n := range seen {
		ns = append(ns, n)
	}
	return ns
}

// ReachableBFS 朴素参照：在当前存在边集上判定 x 到 y 是否有长度 ≥1 的路径。
// 起点不预置为已访问：只有真正沿边再次到达 x 才算 x 可达自身（环/自环语义）。
func (g *Graph) ReachableBFS(x, y string) bool {
	seen := map[string]struct{}{}
	q := []string{}
	for _, n := range g.Out(x) {
		seen[n], q = struct{}{}, append(q, n)
	}
	for len(q) > 0 {
		cur := q[0]
		q = q[1:]
		if cur == y {
			return true
		}
		for _, n := range g.Out(cur) {
			if _, ok := seen[n]; !ok {
				seen[n] = struct{}{}
				q = append(q, n)
			}
		}
	}
	return false
}

// NaivePairs 朴素参照：对每对节点用 ReachableBFS 判定，返回按字典序排序的全部可达对。
func (g *Graph) NaivePairs() [][2]string {
	ns := g.Nodes()
	var ps [][2]string
	for _, x := range ns {
		for _, y := range ns {
			if g.ReachableBFS(x, y) {
				ps = append(ps, [2]string{x, y})
			}
		}
	}
	sort.Slice(ps, func(i, j int) bool {
		return ps[i][0] < ps[j][0] || ps[i][0] == ps[j][0] && ps[i][1] < ps[j][1]
	})
	return ps
}
