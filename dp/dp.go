// Package dp 用滚动行 DP 计算参照串与查询串的最长公共子串。
// 空间 O(min(|a|,|b|))，不依赖其他包。
package dp

import "sync"

// Core 持有固定参照串 a，可并发安全地应答多次 Longest。
type Core struct {
	a string

	mu sync.Mutex
	// cells 是非导出计数器：最近一次 Longest 实际保留的 DP 单元数
	// （滚动行大小 = min(|a|,|b|)+1）。只供包内测试直接读取，
	// 不出现在任何公开接口里。
	cells int
}

// New 返回以 a 为参照串的 Core。
func New(a string) *Core { return &Core{a: a} }

// Longest 返回 a 与 b 的最长公共子串的长度与它在 a 中的起始下标。
// 长度最大者有多个时，起始下标取最小。
func (c *Core) Longest(b string) (length, start int) {
	n, m := len(c.a), len(b)
	if m <= n {
		length, start = roll(c.a, b, true) // 外层扫 a，滚动行覆盖 b
	} else {
		length, start = roll(b, c.a, false) // 外层扫 b，滚动行覆盖 a
	}
	c.mu.Lock()
	c.cells = min(n, m) + 1
	c.mu.Unlock()
	return length, start
}

// roll 以 x 为外层、y 为滚动行做 DP。aIsOuter 表示参照串 a 是否
// 是外层串 x，用于把命中位置换算回 a 中的起始下标。
func roll(x, y string, aIsOuter bool) (best, start int) {
	row := make([]int, len(y)+1)
	for i := 1; i <= len(x); i++ {
		diag := 0 // dp[i-1][j-1]
		for j := 1; j <= len(y); j++ {
			prev := row[j]
			if x[i-1] == y[j-1] {
				row[j] = diag + 1
			} else {
				row[j] = 0
			}
			diag = prev
			endInA := j // a 是内层 y 时，结束位置取行内下标
			if aIsOuter {
				endInA = i
			}
			if cand := row[j]; cand > best || (cand == best && cand > 0 && endInA-cand < start) {
				best = cand
				start = endInA - cand
			}
		}
	}
	return best, start
}
