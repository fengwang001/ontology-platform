package blame

import "strings"

// splitLines 把文件内容切分为行：按 "\n" 切分，末尾换行不产生额外空行。
func splitLines(content string) []string {
	if content == "" {
		return nil
	}
	lines := strings.Split(content, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// alignLines 计算 child 与 parent 两份内容之间的行对应关系。
//
// 返回切片与 child 等长，res[i] 是 child 第 i 行配对到的 parent 行号（0 起始），
// 未配对时为 -1。配对保持相对次序、配对行数最多（LCS 长度）；
// 最多配对方案不唯一时，取配对下标序列字典序最小的方案，
// 即「靠前的行优先配对」，结果唯一。行相等按逐字节比较。
func alignLines(child, parent []string) []int {
	n, m := len(child), len(parent)
	res := make([]int, n)
	for i := range res {
		res[i] = -1
	}
	if n == 0 || m == 0 {
		return res
	}

	// lcs[i][j] 为 child[i:] 与 parent[j:] 的最长公共子序列长度。
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			best := lcs[i+1][j]
			if lcs[i][j+1] > best {
				best = lcs[i][j+1]
			}
			if child[i] == parent[j] && lcs[i+1][j+1]+1 > best {
				best = lcs[i+1][j+1] + 1
			}
			lcs[i][j] = best
		}
	}

	// 贪心构造字典序最小的配对序列：每次取可行的最小 (i, j)。
	// (i, j) 可行当且仅当 child[i]==parent[j] 且 lcs[i][j]==k（k 为剩余对数），
	// 此时必有 lcs[i+1][j+1]==k-1，配对后仍可完成最优。
	i, j, k := 0, 0, lcs[0][0]
	for k > 0 {
		advanced := false
		for ii := i; ii < n && !advanced; ii++ {
			for jj := j; jj < m; jj++ {
				if child[ii] == parent[jj] && lcs[ii][jj] == k {
					res[ii] = jj
					i, j, k = ii+1, jj+1, k-1
					advanced = true
					break
				}
			}
		}
		if !advanced {
			break // 不可达：lcs 保证可行对存在
		}
	}
	return res
}
