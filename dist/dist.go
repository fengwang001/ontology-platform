// Package dist 实现带距离上限 k 的 Levenshtein 编辑距离（band 剪枝）。
package dist

import (
	"errors"
	"sync/atomic"
)

// ErrExceedsCap 表示真实编辑距离大于上限 k（不返回具体值）。
var ErrExceedsCap = errors.New("dist: edit distance exceeds cap")

// inf 表示「> k，不细算」的哨兵，远大于任何真实距离。
const inf = 1 << 30

// cells 是非导出计数器：最近一次 Distance 实际计算过的 DP 单元数。
// 只在本包内部与包内测试可见，不出现在任何公开接口。
var cells atomic.Int64

// Distance 返回把 a 变成 b 的最小编辑代价；真实距离 > k 时返回 ErrExceedsCap。
// 只计算 |i-j| <= k 的对角线带宽内的单元，带宽外视为 inf，不改变 <= k 时的结果。
func Distance(a, b string, k int) (int, error) {
	n, m := len(a), len(b)
	if n-m > k || m-n > k {
		cells.Store(0)
		return 0, ErrExceedsCap
	}
	prev := make([]int, m+1)
	cur := make([]int, m+1)
	for j := range prev {
		prev[j] = inf
		cur[j] = inf
	}
	count := 0
	// 第 0 行：dp[0][j] = j，只填带宽内。
	hi := k
	if hi > m {
		hi = m
	}
	for j := 0; j <= hi; j++ {
		prev[j] = j
		count++
	}
	for i := 1; i <= n; i++ {
		lo := i - k
		if lo < 0 {
			lo = 0
		}
		hi := i + k
		if hi > m {
			hi = m
		}
		// 显式把本行带宽两侧的光环重置为 inf：
		// cur 两行前曾被复用，带宽随 i 平移，stale 值必须先清掉再算。
		if lo > 0 {
			cur[lo-1] = inf
		}
		if hi < m {
			cur[hi+1] = inf
		}
		for j := lo; j <= hi; j++ {
			if j == 0 {
				cur[j] = i // 边界 dp[i][0] = i（仅当 0 在带宽内）
			} else {
				best := prev[j] + 1              // 删除 a[i-1]
				if v := cur[j-1] + 1; v < best { // 插入 b[j-1]
					best = v
				}
				sub := prev[j-1] // 替换/匹配
				if a[i-1] != b[j-1] {
					sub++
				}
				if sub < best {
					best = sub
				}
				cur[j] = best
			}
			count++
		}
		prev, cur = cur, prev
	}
	cells.Store(int64(count))
	if prev[m] > k {
		return 0, ErrExceedsCap
	}
	return prev[m], nil
}
