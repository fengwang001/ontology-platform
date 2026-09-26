// Package gram 计算正规方程的左端 Gram 矩阵 AᵀA 与右端 Aᵀb。
// 矩阵 A 以行主序一维切片存储（m×n），本包不依赖其他包。
package gram

// Gram 返回 G = AᵀA，n×n 行主序一维切片。
// G[i][j] = Σ_k A[k][i]·A[k][j]（第 i 列与第 j 列的列点积）。
// 点积对 i≤j 只计算一次，然后镜像写入 (i,j) 与 (j,i)，
// 因此两个位置逐位相等，Gram 严格对称。
func Gram(a []float64, m, n int) []float64 {
	g := make([]float64, n*n)
	for i := 0; i < n; i++ {
		for j := i; j < n; j++ {
			var s float64
			for k := 0; k < m; k++ {
				s += a[k*n+i] * a[k*n+j]
			}
			g[i*n+j] = s
			g[j*n+i] = s
		}
	}
	return g
}

// AtB 返回 c = Aᵀb，长度 n。
// c[i] = Σ_k A[k][i]·b[k]。
func AtB(a, b []float64, m, n int) []float64 {
	c := make([]float64, n)
	for i := 0; i < n; i++ {
		var s float64
		for k := 0; k < m; k++ {
			s += a[k*n+i] * b[k]
		}
		c[i] = s
	}
	return c
}
