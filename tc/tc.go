// Package tc 用布尔 Floyd–Warshall 计算有向图的传递闭包（可达性）。
package tc

import (
	"sync/atomic"

	"ontology/dg"
)

// Closure 是预计算好的传递闭包，Reach 以 O(1) 回答。
type Closure struct {
	reach [][]bool
	// lastChecked 记录最近一次 Reach 查询检查过的节点个数。
	// 非导出字段，不出现在任何公开接口里。
	lastChecked atomic.Int64
}

// Compute 对 g 做一次布尔 Floyd–Warshall，返回闭包。
// reach[i][j] 初始为直接边（对角线为 false），
// 对每个中间节点 k 做 reach[i][j] ||= reach[i][k] && reach[k][j]。
func Compute(g *dg.Graph) *Closure {
	n := g.N()
	reach := make([][]bool, n)
	for i := range reach {
		reach[i] = make([]bool, n)
		for j := 0; j < n; j++ {
			reach[i][j] = g.HasEdge(i, j)
		}
	}
	for k := 0; k < n; k++ {
		for i := 0; i < n; i++ {
			if !reach[i][k] {
				continue
			}
			for j := 0; j < n; j++ {
				reach[i][j] = reach[i][j] || reach[k][j]
			}
		}
	}
	return &Closure{reach: reach}
}

// Reach 报告是否存在从 i 到 j 的、至少含 1 条边的有向路径。
// 只查预计算矩阵的一个格子，检查节点个数恒为 1，与图规模无关。
// i 或 j 越界时返回 false。
func (c *Closure) Reach(i, j int) bool {
	if i < 0 || i >= len(c.reach) || j < 0 || j >= len(c.reach) {
		c.lastChecked.Store(0)
		return false
	}
	c.lastChecked.Store(1)
	return c.reach[i][j]
}
