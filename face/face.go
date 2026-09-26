// Package face 按 next 规则追踪闭合面、统计连通分量、支撑欧拉公式校验。
// 依赖 pg。
package face

import (
	"strconv"

	"ontology/pg"
)

// Tracer 沿 next 规则走有向边。checks 是非导出计数器：记录最近一次
// next 定位为在环序中找前驱而检查过的邻接节点个数（索引定位恒为 1）。
// 它不出现在任何公开接口里。
type Tracer struct {
	checks int
}

// next 走一条：u→v 的下一条是 v→Prev(v,u)。
func (t *Tracer) next(g *pg.Graph, u, v int) (int, int, bool) {
	w, ok := g.Prev(v, u)
	t.checks++ // 索引定位只检查 1 个槽位，与度数无关
	return v, w, ok
}

// Trace 从每条未访问的有向边出发走 next 直到回到起点，收集全部面。
// 每条有向边恰好属于一个面，故无需去重。返回面（顶点序列）列表。
func Trace(g *pg.Graph) [][]int {
	t := &Tracer{}
	n := g.N()
	seen := make(map[[2]int]struct{}, 2*g.EdgeCount())
	var faces [][]int
	for u := 0; u < n; u++ {
		for _, v := range g.Neighbors(u) {
			if _, done := seen[[2]int{u, v}]; done {
				continue
			}
			var face []int
			a, b := u, v
			for {
				seen[[2]int{a, b}] = struct{}{}
				face = append(face, a)
				na, nb, ok := t.next(g, a, b)
				if !ok { // 环序缺失/损坏，终止该面
					break
				}
				a, b = na, nb
				if a == u && b == v {
					break
				}
			}
			faces = append(faces, face)
		}
	}
	return faces
}

// NaiveFaces 朴素参照：线性扫描定位前驱，逐有向边走回起点，
// 对闭合行走按循环移位的字典序最小代表去重。用于对照 Trace 的结果。
func NaiveFaces(g *pg.Graph) [][]int {
	seen := map[[2]int]bool{}
	dedup := map[string]bool{}
	var out [][]int
	for u := 0; u < g.N(); u++ {
		for _, v := range g.Neighbors(u) {
			if seen[[2]int{u, v}] {
				continue
			}
			var walk []int
			a, b := u, v
			for !seen[[2]int{a, b}] {
				seen[[2]int{a, b}] = true
				walk = append(walk, a)
				rot := g.Rotation(b)
				i := 0
				for i < len(rot) && rot[i] != a { // 线性扫描，O(度)
					i++
				}
				a, b = b, rot[(i-1+len(rot))%len(rot)]
			}
			if key := canonical(walk); !dedup[key] {
				dedup[key] = true
				out = append(out, walk)
			}
		}
	}
	return out
}

// canonical 取闭合行走所有循环移位中字典序最小者，作为去重键。
func canonical(w []int) string {
	best := ""
	for s := range w {
		cur := ""
		for k := range w {
			cur += strconv.Itoa(w[(s+k)%len(w)]) + ","
		}
		if best == "" || cur < best {
			best = cur
		}
	}
	return best
}

// Components 用并查集统计连通分量数（孤立点各自成一个分量）。
func Components(g *pg.Graph) int {
	n := g.N()
	parent := make([]int, n)
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(x int) int {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}
	for u := 0; u < n; u++ {
		for _, v := range g.Neighbors(u) {
			ru, rv := find(u), find(v)
			if ru != rv {
				parent[ru] = rv
			}
		}
	}
	c := 0
	for i := 0; i < n; i++ {
		if find(i) == i {
			c++
		}
	}
	return c
}
