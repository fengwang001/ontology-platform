// Package closure 在 graph 上增量维护可达对集合 R：加边并入、删边 DRed（过删+再推导）。
package closure

import (
	"cmp"
	"slices"

	"ontology/graph"
)

// Closure 是可达对集合 R 及其按节点的正向/反向索引。不保证并发安全，由上层加锁。
type Closure struct {
	g        *graph.Graph
	fwd, rev map[string]map[string]bool // fwd[x]=可从 x 到达的集；rev[y]=可到达 y 的集
	n        int                        // |R|
	checked  int                        // 最近一次增删边检查过的可达对个数（非导出，仅同包测试可观）
}

// New 在图 g 上建立空闭包。
func New(g *graph.Graph) *Closure {
	return &Closure{g: g, fwd: make(map[string]map[string]bool), rev: make(map[string]map[string]bool)}
}

func (c *Closure) add(x, y string) {
	if c.fwd[x] == nil {
		c.fwd[x] = make(map[string]bool)
	}
	if c.rev[y] == nil {
		c.rev[y] = make(map[string]bool)
	}
	if c.fwd[x][y] {
		return
	}
	c.fwd[x][y] = true
	c.rev[y][x] = true
	c.n++
}

func (c *Closure) del(x, y string) {
	delete(c.fwd[x], y)
	delete(c.rev[y], x)
	c.n--
}

// ends 返回 {n} ∪ 索引端点：fwd=false 取 {x:(x,n)∈R}，否则取 {y:(n,y)∈R}。
func (c *Closure) ends(n string, fwd bool) []string {
	idx := c.rev[n]
	if fwd {
		idx = c.fwd[n]
	}
	out := []string{n}
	for m := range idx {
		if m != n {
			out = append(out, m)
		}
	}
	return out
}

// AddEdge 在边 (u,v) 由不存在变为存在后，把新产生的可达对并入 R。
func (c *Closure) AddEdge(u, v string) {
	xs, ys := c.ends(u, false), c.ends(v, true)
	c.checked = len(xs) * len(ys)
	for _, x := range xs {
		for _, y := range ys {
			c.add(x, y)
		}
	}
}

// RemoveEdge 在边 (u,v) 消失后执行 DRed：先过删再逐对再推导。返回过删数与再推导数。
func (c *Closure) RemoveEdge(u, v string) (int, int) {
	xs, ys := c.ends(u, false), c.ends(v, true)
	var d [][2]string
	for _, x := range xs {
		for _, y := range ys {
			if c.fwd[x][y] {
				d = append(d, [2]string{x, y})
			}
		}
	}
	for _, p := range d {
		c.del(p[0], p[1])
	}
	re := 0
	for _, p := range d { // 再推导：删边后的图中仍存在 x→y 路径则加回
		found := false
		bfs(c.g, p[0], func(n string) bool {
			if n == p[1] {
				found = true
			}
			return !found
		})
		if found {
			c.add(p[0], p[1])
			re++
		}
	}
	c.checked = len(xs)*len(ys) + len(d)
	return len(d), re
}

// bfs 从 src 遍历 g，对每个经长度≥1路径可达的节点调用 visit；visit 返回 false 时停止。
func bfs(g *graph.Graph, src string, visit func(string) bool) {
	seen := map[string]bool{src: true}
	queue := []string{src}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, nxt := range g.Out(cur) {
			if !visit(nxt) {
				return
			}
			if !seen[nxt] {
				seen[nxt] = true
				queue = append(queue, nxt)
			}
		}
	}
}

// Reachable 报告 (x,y) 是否在 R 中。
func (c *Closure) Reachable(x, y string) bool { return c.fwd[x][y] }

// Pairs 返回 R 中全部可达对，按 (x,y) 字典序排列。
func (c *Closure) Pairs() [][2]string {
	out := make([][2]string, 0, c.n)
	for x, ys := range c.fwd {
		for y := range ys {
			out = append(out, [2]string{x, y})
		}
	}
	slices.SortFunc(out, func(a, b [2]string) int {
		return cmp.Or(cmp.Compare(a[0], b[0]), cmp.Compare(a[1], b[1]))
	})
	return out
}

// Naive 朴素参照：对 g 中每个节点做一次 BFS，返回完整可达对集合。
func Naive(g *graph.Graph) map[[2]string]bool {
	out := make(map[[2]string]bool)
	for _, s := range g.Nodes() {
		bfs(g, s, func(n string) bool {
			out[[2]string{s, n}] = true
			return true
		})
	}
	return out
}
