// Package pivot 实现高斯消元的部分主元选择。
package pivot

import "sync/atomic"

// extraScans 记录「为判定某列奇异而额外扫描的列条目数」。
// 正确实现把「找最大」与「判全零」合并在同一趟扫描里，该计数恒为 0。
// 非导出字段，不出现在任何公开接口中。
var extraScans atomic.Int64

// Pick 在第 k 列的第 k..n-1 行中找 |a[i*n+k]| 最大的行，
// 并列最大值时取下标最小的一行；该段全为零时 ok=false（列奇异）。
// 单趟扫描同时完成找最大与判全零：最大值非零当且仅当该段不全为零。
func Pick(a []float64, n, k int) (row int, ok bool) {
	best := k
	max := abs(a[k*n+k])
	for i := k + 1; i < n; i++ {
		if v := abs(a[i*n+k]); v > max { // 严格大于：并列时保留先出现的小下标
			best, max = i, v
		}
	}
	if max == 0 {
		return -1, false
	}
	return best, true
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
