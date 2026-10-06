package lineage

import (
	"sort"
	"strings"
)

// splitLines 按 '\n' 切分内容；末尾换行不产生空行；空内容没有行。
// 行比较为逐字节相等，不做任何归一化。
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, "\n")
	if parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

// align 计算 prev（父提交文件）到 curr（本提交文件）的行对应关系，
// 返回 curr 每一行在 prev 中配对行的下标，未配对为 -1。
//
// 配对规则：保持相对次序、配对行数最多；方案不唯一时取配对序列
// 按 (prev 下标, curr 下标) 字典序最小者，即「靠前的行优先配对」，
// 结果唯一确定。
func align(prev, curr []string) []int {
	m, n := len(prev), len(curr)
	res := make([]int, n)
	for j := range res {
		res[j] = -1
	}
	if m == 0 || n == 0 {
		return res
	}
	// dp[i][j] = prev[i:] 与 curr[j:] 的最长公共子序列长度。
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}
	for i := m - 1; i >= 0; i-- {
		for j := n - 1; j >= 0; j-- {
			switch {
			case prev[i] == curr[j]:
				dp[i][j] = dp[i+1][j+1] + 1
			case dp[i+1][j] >= dp[i][j+1]:
				dp[i][j] = dp[i+1][j]
			default:
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	// 字典序最小的配对序列: 按 prev 下标从小到大, 若 prev[i] 能作为某个
	// 最优配对的首行, 则与 curr 中可行且最靠前的行配对。
	// 可证明只需检查 curr 中等于 prev[i] 的最靠前位置 j':
	// 配对可行当且仅当 1+dp[i+1][j'+1] == dp[i][j]。
	pos := make(map[string][]int)
	for j, s := range curr {
		pos[s] = append(pos[s], j)
	}
	i, j := 0, 0
	for i < m && j < n && dp[i][j] > 0 {
		lst := pos[prev[i]]
		k := sort.SearchInts(lst, j)
		if k < len(lst) && 1+dp[i+1][lst[k]+1] == dp[i][j] {
			res[lst[k]] = i
			i++
			j = lst[k] + 1
			continue
		}
		i++
	}
	return res
}
