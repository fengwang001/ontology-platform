// Package lpath 在 wdag 上求全局最长路径：拓扑序 DP、回溯重建、并列取字典序最小。
package lpath

import (
	"errors"

	"ontology/wdag"
)

// ErrNoPath 表示不存在任何至少含 1 条边的路径（如 E=0）。
var ErrNoPath = errors.New("lpath: no path with at least one edge")

const negInf = int64(-1) << 62

// Solver 求最长路径。lastChecks 是非导出计数器：最近一次为求某节点的
// dist 而枚举它的直接前驱时检查过的节点个数。Solver 不可并发共用，一调用一实例。
type Solver struct {
	lastChecks int
}

// NewSolver 返回一个新 Solver。
func NewSolver() *Solver { return &Solver{} }

// Solve 是便捷包装：内部自建 Solver，可安全并发调用。
func Solve(g *wdag.Graph) (int64, []int, error) { return NewSolver().Solve(g) }

// Solve 返回全局最长路径的总权与节点序列；含环返回 wdag.ErrCycle，无边返回 ErrNoPath。
func (s *Solver) Solve(g *wdag.Graph) (int64, []int, error) {
	if g.HasCycle() {
		return 0, nil, wdag.ErrCycle
	}
	n := g.N()
	order := topo(g)

	// DP：dist[v] = 以 v 为终点、至少含 1 条边的最长总权；源为 0（空前缀）。
	// 路径起点任意：前驱 dist 为负时按 max(dist,0) 松弛（从该节点新起路径），
	// 否则非源节点的负 dist 会污染后继（不变量 2/3 由 TestBruteForce 钉住）。
	dist := make([]int64, n)
	for v := 0; v < n; v++ {
		if len(g.In(v)) == 0 {
			dist[v] = 0
		} else {
			dist[v] = negInf
		}
	}
	for _, v := range order {
		checks := 0
		for _, e := range g.In(v) { // 按入度枚举直接前驱，不扫描全部节点
			checks++
			if cand := max(dist[e.From], 0) + e.W; cand > dist[v] {
				dist[v] = cand
			}
		}
		s.lastChecks = checks
	}

	// 答案 = 入度>0 节点的 dist 最大值（对应路径至少含 1 条边）。
	best := negInf
	for v := 0; v < n; v++ {
		if len(g.In(v)) > 0 && dist[v] > best {
			best = dist[v]
		}
	}
	if best == negInf {
		return 0, nil, ErrNoPath
	}

	// good[v]：从 v（到达和恰为 dist[v]）能沿 dist 一致的边走到终点（dist==W 且入度>0）。
	good := make([]bool, n)
	for i := len(order) - 1; i >= 0; i-- {
		v := order[i]
		if dist[v] == best && len(g.In(v)) > 0 {
			good[v] = true
			continue
		}
		for _, e := range g.Out(v) {
			if dist[e.To] == dist[v]+e.W && good[e.To] {
				good[v] = true
				break
			}
		}
	}

	// 前向贪心重建字典序最小序列：每步取可走的最小编号节点。
	// 起点处是"新起路径"，实际和 base=0（用 h）；之后到达和必须恰等于 dist。
	start := -1
	for v := 0; v < n; v++ {
		if dist[v] <= 0 && hasGoodOut(g, dist, good, v) {
			start = v
			break
		}
	}
	path := []int{start}
	base := int64(0)
	for cur := start; dist[cur] != best || len(path) < 2; {
		next := -1
		for _, e := range g.Out(cur) {
			if dist[e.To] == base+e.W && good[e.To] && (next == -1 || e.To < next) {
				next = e.To
			}
		}
		cur = next
		base = dist[cur]
		path = append(path, cur)
	}
	return best, path, nil
}

// hasGoodOut 报告 v 是否可"新起路径"：有出边使到达和（空前缀 0+权）恰为 dist 且通往 good 节点。
func hasGoodOut(g *wdag.Graph, dist []int64, good []bool, v int) bool {
	for _, e := range g.Out(v) {
		if dist[e.To] == max(dist[v], 0)+e.W && good[e.To] {
			return true
		}
	}
	return false
}

// topo 用 Kahn 算法返回拓扑序；调用前须保证无环。
func topo(g *wdag.Graph) []int {
	n := g.N()
	indeg := make([]int, n)
	for v := 0; v < n; v++ {
		indeg[v] = len(g.In(v))
	}
	var queue []int
	for v, d := range indeg {
		if d == 0 {
			queue = append(queue, v)
		}
	}
	order := make([]int, 0, n)
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		order = append(order, u)
		for _, e := range g.Out(u) {
			indeg[e.To]--
			if indeg[e.To] == 0 {
				queue = append(queue, e.To)
			}
		}
	}
	return order
}
