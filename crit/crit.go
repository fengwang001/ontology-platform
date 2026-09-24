// Package crit 在 dag 最长路结果上重建关键路径并识别瓶颈算子。
// 它只依赖 dag 包，不依赖 api。
package crit

import (
	"ontology/dag"
)

// Result 是一次关键路径分析的结果。
type Result struct {
	EndToEnd       int64
	Path           []string // 一条达到 EndToEnd 的源→汇路径（沿前驱回溯）
	Bottleneck     string   // 关键路径上 lat 最大者；并列取名字典序最小
	BottleneckLat  int64
	OnCriticalPath map[string]bool // 位于任意一条关键路径上的算子
}

// Analyze 计算关键路径与瓶颈。图不满足恰一源一汇时透传 dag 的哨兵错误。
func Analyze(g *dag.Graph) (*Result, error) {
	sol, err := g.Solve()
	if err != nil {
		return nil, err
	}

	lat, succ := g.Snapshot()

	// back(v) = 从 v 到汇的最长路径（含 v 与汇）：拓扑逆序单遍 DP。
	back := map[string]int64{sol.Sink: lat[sol.Sink]}
	for i := len(sol.Topo) - 1; i >= 0; i-- {
		v := sol.Topo[i]
		if v == sol.Sink {
			continue
		}
		best := int64(-1)
		for _, w := range succ[v] {
			if c := lat[v] + back[w]; c > best {
				best = c
			}
		}
		back[v] = best
	}

	// v 在某条关键路径上  <=>  fwd(v)+back(v)-lat(v) == E
	on := map[string]bool{}
	bn, bnLat := "", int64(-1)
	for _, v := range sol.Topo {
		if sol.Dist[v]+back[v]-lat[v] == sol.EndToEnd {
			on[v] = true
			if lat[v] > bnLat || (lat[v] == bnLat && (bn == "" || v < bn)) {
				bn, bnLat = v, lat[v]
			}
		}
	}

	// 沿前驱回溯重建一条关键路径。
	path := []string{}
	cur := sol.Sink
	for {
		path = append(path, cur)
		if cur == sol.Source {
			break
		}
		cur = sol.Pred[cur]
	}
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}

	return &Result{
		EndToEnd:       sol.EndToEnd,
		Path:           path,
		Bottleneck:     bn,
		BottleneckLat:  bnLat,
		OnCriticalPath: on,
	}, nil
}

// BruteEndToEnd 是朴素参照：枚举每条源→汇路径，对路径上算子 lat 求和取最大。
// 与 Analyze 的 DP 结果比对即可验证「与朴素参照一致」。
func BruteEndToEnd(g *dag.Graph) (int64, error) {
	sol, err := g.Solve()
	if err != nil {
		return 0, err
	}
	lat, succ := g.Snapshot()
	best := int64(-1)
	var dfs func(v string, acc int64)
	dfs = func(v string, acc int64) {
		acc += lat[v]
		if v == sol.Sink {
			best = max(best, acc)
			return
		}
		for _, w := range succ[v] {
			dfs(w, acc)
		}
	}
	dfs(sol.Source, 0)
	return best, nil
}

// IndependentDist 独立地按拓扑序重算最长路距离（只用 Solve 的拓扑序，不复用其 Dist），
// 供交叉核验 DP 不变量 dist(v)=max(dist(u)+lat(v))，源 dist=lat(源)。
func IndependentDist(g *dag.Graph) (map[string]int64, error) {
	sol, err := g.Solve()
	if err != nil {
		return nil, err
	}
	lat, succ := g.Snapshot()
	d := map[string]int64{sol.Source: lat[sol.Source]}
	for _, u := range sol.Topo {
		for _, v := range succ[u] {
			d[v] = max(d[v], d[u]+lat[v]) // lat>=0：未设置的 0 不会压过真实最长路
		}
	}
	return d, nil
}
