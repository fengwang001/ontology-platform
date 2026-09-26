// Package pivot 提供高斯消元的部分主元选择。
package pivot

import "math"

// Pick 在 n×n 行主序矩阵 a 的第 k 列、第 k..n-1 行中，
// 返回 |a[i][k]| 最大的行下标；并列最大值取下标最小的一行。
// 若该列第 k..n-1 行全为零，返回 ok=false（列奇异）。
func Pick(a []float64, n, k int) (row int, ok bool) {
	best := -1
	var max float64
	for i := k; i < n; i++ {
		v := math.Abs(a[i*n+k])
		if best < 0 || v > max {
			best, max = i, v
		}
	}
	if best < 0 || max == 0 {
		return 0, false
	}
	return best, true
}
