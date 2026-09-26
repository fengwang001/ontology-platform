// Package power 提供幂迭代的单步原语：矩阵-向量乘法、无穷范数、Rayleigh 商。
// 所有函数只读输入，不依赖其他包。
package power

// MatVec 计算 w = A·v，A 为 n×n 行主序矩阵。返回新分配的切片，不修改输入。
func MatVec(a, v []float64, n int) []float64 {
	w := make([]float64, n)
	for i := 0; i < n; i++ {
		var s float64
		row := a[i*n : i*n+n]
		for j := 0; j < n; j++ {
			s += row[j] * v[j]
		}
		w[i] = s
	}
	return w
}

// InfNorm 返回 max|w_i|（非负）。
func InfNorm(w []float64) float64 {
	var m float64
	for _, x := range w {
		if ax := abs(x); ax > m {
			m = ax
		}
	}
	return m
}

// Rayleigh 返回 Rayleigh 商 (vᵀ·A·v)/(vᵀ·v)。
func Rayleigh(a, v []float64, n int) float64 {
	av := MatVec(a, v, n)
	return dot(v, av) / dot(v, v)
}

func dot(x, y []float64) float64 {
	var s float64
	for i := range x {
		s += x[i] * y[i]
	}
	return s
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
