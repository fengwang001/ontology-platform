// Package tc 用 Floyd–Warshall（布尔，bitset 行）计算有向图传递闭包，
// Compute 之后 Reach O(1) 回答。依赖 dg。
package tc

import (
	"sync"
	"sync/atomic"

	"ontology/dg"
)

// TC 持有一份图快照的闭包矩阵：reach[i] 是第 i 行的 bitset，
// 第 j 位表示是否存在 i→j 的至少含 1 条边的路径。
type TC struct {
	n     int
	reach [][]uint64
	mu    sync.RWMutex
	// lastChecks 记录最近一次 Reach 查询检查过的"单元"数：
	// 矩阵查询只测一个预计算位、不扫描任何节点，故恒为 1，与 n 无关。
	// 非导出：只允许同包白盒测试直接读，不经过任何导出接口。
	lastChecks atomic.Int64
}

// New 对 g 做快照：reach[i][j] 初始为是否存在直接边 i→j，对角线置 false。
func New(g *dg.Graph) *TC {
	n := g.N()
	words := (n + 63) >> 6
	reach := make([][]uint64, n)
	for i := 0; i < n; i++ {
		row := make([]uint64, words)
		for j := 0; j < n; j++ {
			if g.HasEdge(i, j) { // 对角线：无自环边，自然为 false
				row[j>>6] |= uint64(1) << uint(j&63)
			}
		}
		reach[i] = row
	}
	return &TC{n: n, reach: reach}
}

// Compute 一次性运行 Floyd–Warshall 布尔闭包：
// 依次以 k 为中间节点，若 i 可达 k 则 i 的可达集并入 k 的可达集。
// 对角线与普通格同规则更新——环上节点由此得到 Reach(i,i)=true。
// 重复调用幂等（闭包的闭包仍是自身）。
func (t *TC) Compute() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for k := 0; k < t.n; k++ {
		kw, km := k>>6, uint64(1)<<uint(k&63)
		rk := t.reach[k]
		for i := 0; i < t.n; i++ {
			if t.reach[i][kw]&km != 0 {
				ri := t.reach[i]
				for w := range rk {
					ri[w] |= rk[w]
				}
			}
		}
	}
}

// Reach 回答是否存在 i→j 的至少含 1 条边的路径。
// Compute 后为纯矩阵位查询：只检查 1 个预计算单元，不扫描节点，O(1)。
// 调用方须保证 i,j ∈ [0,n)。
func (t *TC) Reach(i, j int) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	t.lastChecks.Store(1)
	return t.reach[i][j>>6]&(uint64(1)<<uint(j&63)) != 0
}
