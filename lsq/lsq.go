// Package lsq 在已有正规方程 G·x = c 上做无主元高斯消元与回代。
// G 为 n×n 行主序一维切片，必须按完整对称矩阵对待。
package lsq

import "math"

// singularTol 是相对零阈值：消元主元 |pivot| ≤ singularTol·max|Gij| 即视为奇异。
const singularTol = 1e-12

// IsSingular 在 G 的副本上跑无主元消元，判定 G 是否奇异（秩亏）。
// 不修改调用方传入的 G。
func IsSingular(g []float64, n int) bool {
	var scale float64
	w := make([]float64, n*n)
	copy(w, g)
	for _, v := range g {
		if a := math.Abs(v); a > scale {
			scale = a
		}
	}
	for k := 0; k < n; k++ {
		piv := w[k*n+k]
		if math.Abs(piv) <= singularTol*scale {
			return true
		}
		for i := k + 1; i < n; i++ {
			f := w[i*n+k] / piv
			for j := k; j < n; j++ {
				w[i*n+j] -= f * w[k*n+j]
			}
		}
	}
	return false
}

// SolveG 用无主元消元再回代解 G·x = c，返回 x（长度 n）。
// 求解全程在 G、c 的副本上进行，不修改输入；调用方须保证 G 非奇异
// （api 在 Factor 时已用 IsSingular 拒绝秩亏）。
func SolveG(g, c []float64, n int) []float64 {
	w := make([]float64, n*n)
	copy(w, g)
	d := make([]float64, n)
	copy(d, c)

	for k := 0; k < n; k++ {
		piv := w[k*n+k]
		for i := k + 1; i < n; i++ {
			f := w[i*n+k] / piv
			// 完整矩阵逐列消元（含下三角），不利用对称性跳项。
			for j := k; j < n; j++ {
				w[i*n+j] -= f * w[k*n+j]
			}
			d[i] -= f * d[k]
		}
	}

	x := make([]float64, n)
	for i := n - 1; i >= 0; i-- {
		s := d[i]
		for j := i + 1; j < n; j++ {
			s -= w[i*n+j] * x[j]
		}
		x[i] = s / w[i*n+i]
	}
	return x
}
