package editdist

import "slices"

// inf 是“带外/不可达”的哨兵值，远大于任何可能的真实距离。
const inf = 1 << 30

// banded 在 |i-j| <= k 的对角线带内做 Levenshtein 动态规划。
//
// 正确性依据：任意一条代价 <= k 的编辑路径，其经过的每个格子 (i,j)
// 都满足 |i-j| <= k，因此带内算出的值与完整 DP 完全一致；若带内
// 算出的 d[n][m] <= k，则它就是精确距离，反之真实距离必然 > k。
//
// 内存：先把两侧规范化为 m <= n（m 为较短边），再只保留两行长度
// 为 w = min(2k+1, m+1) 的滚动数组，故工作内存为 O(min(n, m))，
// 在 k 较小时实际为 O(k)。st.MaxWorkLen 记录 w。
//
// 提前终止：
//  1. n-m > k 时不填任何单元直接判定超过 k（CellsFilled == 0）；
//  2. 某一行带内所有格子都 > k 时，最终距离必 > k，立即返回。
//
// 对称性：先把两侧规范化（短边在后；等长时按码点字典序小者在前），
// 因此 (a,b) 与 (b,a) 归一到同一次计算，距离与填充单元数完全一致。
//
// 返回 (distance, exceeded)；exceeded 为 true 时 distance 为 -1。
func banded(ra, rb []rune, k int, st *Stats) (int, bool) {
	n, m := len(ra), len(rb)
	if m > n || (m == n && slices.Compare(ra, rb) > 0) {
		ra, rb = rb, ra
		n, m = m, n
	}
	if n-m > k {
		return -1, true
	}
	w := 2*k + 1
	if m+1 < w {
		w = m + 1
	}
	if w > st.MaxWorkLen {
		st.MaxWorkLen = w
	}
	prev := make([]int, w)
	curr := make([]int, w)

	// 第 0 行：d[0][j] = j，仅 j <= k 在带内。
	for p := range prev {
		prev[p] = inf
	}
	hi0 := k
	if m < hi0 {
		hi0 = m
	}
	for j := 0; j <= hi0; j++ {
		prev[j] = j
		st.CellsFilled++
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
		lop := i - 1 - k
		if lop < 0 {
			lop = 0
		}
		delta := lo - lop // 0 或 1：本行窗口相对上一行的平移量
		for p := range curr {
			curr[p] = inf
		}
		rowMin := inf
		for j := lo; j <= hi; j++ {
			pos := j - lo
			v := i // j == 0 时 d[i][0] = i
			if j > 0 {
				best := inf
				if sp := pos + delta - 1; sp >= 0 { // 替换/匹配 d[i-1][j-1]
					c := 1
					if ra[i-1] == rb[j-1] {
						c = 0
					}
					if val := prev[sp] + c; val < best {
						best = val
					}
				}
				if dp := pos + delta; dp < w { // 删除 d[i-1][j]
					if val := prev[dp] + 1; val < best {
						best = val
					}
				}
				if pos >= 1 { // 插入 d[i][j-1]
					if val := curr[pos-1] + 1; val < best {
						best = val
					}
				}
				v = best
			}
			curr[pos] = v
			st.CellsFilled++
			if v < rowMin {
				rowMin = v
			}
		}
		if rowMin > k {
			return -1, true
		}
		prev, curr = curr, prev
	}

	lon := n - k
	if lon < 0 {
		lon = 0
	}
	d := prev[m-lon]
	if d > k {
		return -1, true
	}
	return d, false
}
