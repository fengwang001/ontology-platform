// Package power 提供幂迭代的单步原语：矩阵-向量乘法、无穷范数、Rayleigh 商。
// 本包不依赖工程内其他包，依赖方向为 power <- eigen <- api。
package power

// MatVec 返回 w = A·v。A 是 n×n 对称矩阵，按行主序存为一维切片。
// 调用方须保证 len(a)==n*n 且 len(v)==n（由上层 eigen.Iterate 统一校验）。
func MatVec(a, v []float64, n int) []float64 {
	w := make([]float64, n)
	for i := 0; i < n; i++ {
		var s float64
		row := i * n
		for j := 0; j < n; j++ {
			s += a[row+j] * v[j]
		}
		w[i] = s
	}
	return w
}

// InfNorm 返回无穷范数 max|w_i|。全零切片返回 0。
func InfNorm(w []float64) float64 {
	m := 0.0
	for _, x := range w {
		if x < 0 {
			x = -x
		}
		if x > m {
			m = x
		}
	}
	return m
}

// Rayleigh 返回 Rayleigh 商 (vᵀ·A·v)/(vᵀ·v)，其中 v 必须非零。
// A·v 只计算一次并复用于分子。
func Rayleigh(a, v []float64, n int) float64 {
	w := MatVec(a, v, n)
	var num, den float64
	for i := range v {
		num += v[i] * w[i]
		den += v[i] * v[i]
	}
	return num / den
}
