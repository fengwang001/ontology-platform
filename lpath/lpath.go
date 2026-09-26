// Package lpath 在 wdag 上求全局最长路径：拓扑序 DP、回溯重建、并列取字典序最小。
package lpath

import (
	"errors"

	"ontology/wdag"
)

// ErrNoPath 表示图中不存在至少含 1 条边的路径（如 E=0）。
var ErrNoPath = errors.New("lpath: 无可用路径")

// Solver 求一张图的最长路径。lastChecks 是非导出计数器：
// 最近一次为求某节点 dist 而枚举其直接前驱时检查过的节点个数。
type Solver struct {
	g          *wdag.Graph
	lastChecks int
}

// New 在图 g 上建求解器。
func New(g *wdag.Graph) *Solver { return &Solver{g: g} }

// Solve 返回全局最长路径的总权与节点序列；含环返回 wdag.ErrCycle，
// 无任何路径返回 ErrNoPath。并列时返回字典序最小的节点序列。
func (s *Solver) Solve() (int64, []int, error) {
	g := s.g
	n := g.N()
	topo, err := g.Topo()
	if err != nil {
		return 0, nil, err
	}
	// dist[v]：以 v 为终点的最长总权。路径起点任意（不变量 2），故每个节点
	// 都提供权 0 的空前缀（对非负权与"源记 0、其余 -inf"的写法逐值一致，
	// 对负权则允许从任意节点重新开始，保证与朴素参照逐值相同）。
	dist := make([]int64, n) // 全 0：空前缀
	for _, v := range topo {
		preds := g.InEdges(v)
		s.lastChecks = len(preds) // 按入度枚举直接前驱，不扫全图
		for _, e := range preds {
			if dist[e.From]+e.W > dist[v] {
				dist[v] = dist[e.From] + e.W
			}
		}
	}
	// 答案：至少含 1 条边，故对每条边取 dist[u]+w（空前缀在 u 处重新开始）。
	var total int64
	have := false
	for u := 0; u < n; u++ {
		for _, e := range g.OutEdges(u) {
			if c := dist[u] + e.W; !have || c > total {
				total, have = c, true
			}
		}
	}
	if !have {
		return 0, nil, ErrNoPath
	}
	// best[v]：从 v 出发（≥0 条边）的最大权，用于回溯时判定可达余量。
	best := make([]int64, n)
	for i := n - 1; i >= 0; i-- {
		v := topo[i]
		for _, e := range g.OutEdges(v) {
			if c := e.W + best[e.To]; c > best[v] {
				best[v] = c
			}
		}
	}
	// 起点：最小的 s，存在边 s→t 使 w+best[t]==total。
	start, rem := -1, total
	for v := 0; v < n && start < 0; v++ {
		for _, e := range g.OutEdges(v) {
			if e.W+best[e.To] == total {
				start = v
				break
			}
		}
	}
	// 回溯：每步在能达到余量的后继中取最小编号；余量归零即停（前缀更短者字典序更小）。
	// 首步无条件执行：路径至少含 1 条边，即使 total 为 0。
	path := []int{start}
	cur := start
	for {
		next, w := -1, int64(0)
		for _, e := range g.OutEdges(cur) {
			if e.W+best[e.To] == rem && (next < 0 || e.To < next) {
				next, w = e.To, e.W
			}
		}
		if next < 0 {
			break
		}
		path = append(path, next)
		rem -= w
		cur = next
		if rem == 0 {
			break
		}
	}
	return total, path, nil
}
