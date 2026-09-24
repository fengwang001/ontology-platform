// Package closure 在带重数有向图之上增量维护可达对集合 R（长度 ≥ 1 的有向路径）。
// 加边按定义并入；删边采用 DRed（过删 + 再推导）。仅依赖 graph 包。
package closure

import (
	"sort"

	"ontology/graph"
)

// Closure 持有图、可达对集合与按节点的正/反向可达索引。非并发安全（api 加锁）。
type Closure struct {
	g   *graph.Graph
	r   map[graph.Edge]struct{}
	out map[string]map[string]struct{} // out[x]：x 能到达的节点
	in  map[string]map[string]struct{} // in[y]：能到达 y 的节点
	// checked 记录最近一次增/删中检查过的可达对个数；非导出，仅包内测试可读。
	checked int
}

// New 创建空闭包。
func New() *Closure {
	return &Closure{g: graph.New(), r: map[graph.Edge]struct{}{},
		out: map[string]map[string]struct{}{}, in: map[string]map[string]struct{}{}}
}

// HasEdge 报告边 (u,v) 当前是否存在。EdgeCount 返回存在边条数。
func (c *Closure) HasEdge(u, v string) bool { return c.g.HasEdge(u, v) }
func (c *Closure) EdgeCount() int           { return c.g.EdgeCount() }

// Reachable 报告 (x,y) 是否属于 R。
func (c *Closure) Reachable(x, y string) bool {
	_, ok := c.r[graph.Edge{U: x, V: y}]
	return ok
}

// Pairs 返回 R 的全部可达对，按 (U,V) 字典序排列。
func (c *Closure) Pairs() []graph.Edge {
	ps := make([]graph.Edge, 0, len(c.r))
	for p := range c.r {
		ps = append(ps, p)
	}
	sort.Slice(ps, func(i, j int) bool {
		return ps[i].U < ps[j].U || ps[i].U == ps[j].U && ps[i].V < ps[j].V
	})
	return ps
}

func (c *Closure) add(p graph.Edge) {
	c.r[p] = struct{}{}
	if c.out[p.U] == nil {
		c.out[p.U] = map[string]struct{}{}
	}
	if c.in[p.V] == nil {
		c.in[p.V] = map[string]struct{}{}
	}
	c.out[p.U][p.V], c.in[p.V][p.U] = struct{}{}, struct{}{}
}

func (c *Closure) del(p graph.Edge) {
	delete(c.r, p)
	delete(c.out[p.U], p.V)
	delete(c.in[p.V], p.U)
}

// members 返回 {seed}∪idx 的去重并集（有环时 seed 也在 idx 中，必须去重）。
func members(seed string, idx map[string]struct{}) []string {
	seen := map[string]struct{}{seed: {}}
	out := []string{seed}
	for n := range idx {
		if _, ok := seen[n]; !ok {
			seen[n], out = struct{}{}, append(out, n)
		}
	}
	return out
}

// AddEdge 使 (u,v) 重数加 1。created 报告边是否从不存在变为存在；仅此时把
// X×Y（X={u}∪能到u者，Y={v}∪v能到者）并入 R；重数再加时 R 不变、checked=0。
func (c *Closure) AddEdge(u, v string) (created bool) {
	if created = c.g.Add(u, v); !created {
		c.checked = 0
		return false
	}
	xset, yset := members(u, c.in[u]), members(v, c.out[v])
	c.checked = len(xset) * len(yset) // 过 X×Y 逐对检查，索引定位，不扫整表
	for _, x := range xset {
		for _, y := range yset {
			p := graph.Edge{U: x, V: y}
			if _, ok := c.r[p]; !ok {
				c.add(p)
			}
		}
	}
	return true
}

// RemoveEdge 使 (u,v) 重数减 1。dropped 报告重数是否降为 0；仅此时执行 DRed：
// 先从 R 删除过删集 D=(X×Y)∩旧R，再把 D 中在新图仍可达者加回（再推导）。
func (c *Closure) RemoveEdge(u, v string) (overDeleted, rederived int, dropped bool) {
	if dropped = c.g.Remove(u, v); !dropped {
		c.checked = 0
		return 0, 0, false
	}
	xset, yset := members(u, c.in[u]), members(v, c.out[v])
	var d []graph.Edge
	for _, x := range xset {
		for _, y := range yset {
			p := graph.Edge{U: x, V: y}
			if _, ok := c.r[p]; ok {
				d = append(d, p)
			}
		}
	}
	overDeleted = len(d)
	for _, p := range d {
		c.del(p)
	}
	for _, p := range d { // 边已从图删除：D 中每对在当前图仍有路径则加回
		if bfsReaches(c.g, p.U, p.V) {
			c.add(p)
			rederived++
		}
	}
	c.checked = len(xset)*len(yset) + len(d)
	return overDeleted, rederived, true
}

// bfsReaches 判定当前图上 x 到 y 是否有长度 ≥ 1 的路径。起点不预置为已访问：
// 只有真正沿边再次到达 x 才算 x 可达自身（环/自环语义）。
func bfsReaches(g *graph.Graph, x, y string) bool {
	seen, q := map[string]struct{}{}, []string{}
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
