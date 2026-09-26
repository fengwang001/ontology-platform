// Package mul 提供朴素三重循环与分块（blocked）两种方阵乘法，
// 二者对同一输入必须给出逐位相等的 float64 结果。
//
// 本包只依赖 blk。
package mul

import "ontology/blk"

// validInputs 判断 n、bs 与行主序一维切片长度是否自洽。
// 非法时上层 api 负责返回哨兵错误；这里只做防御性判定。
func validInputs(a, b []float64, n, bs int) bool {
	if n < 1 || bs < 1 || bs > n {
		return false
	}
	return len(a) == n*n && len(b) == n*n
}

// Naive 是参照实现：行主序一维切片上的朴素三重循环。
// 对固定 (i,j)，k 严格按 0,1,…,n-1 升序累加。
// 行主序下标：M[r][c] = m[r*n+c]。
func Naive(a, b []float64, n int) []float64 {
	if n < 1 || len(a) != n*n || len(b) != n*n {
		return nil
	}
	c := make([]float64, n*n)
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			s := 0.0
			for k := 0; k < n; k++ {
				s += a[i*n+k] * b[k*n+j]
			}
			c[i*n+j] = s
		}
	}
	return c
}

// Blocked 按块分组组织循环：最外层 i 块 → k 块 → j 块，
// 块内仍为 i → k → j；每维都用 blk.Parts 定位，尾块由其左闭右开
// 边界自然覆盖，不跳过也不越界。
//
// 关键序性质：k 维块按块索引升序、块内 k 升序，因此对任意固定
// (i,j)，乘积累加的先后次序恒为 k=0,1,…,n-1——与 Naive 完全一致，
// 分块只改变分组结构，故两者结果逐位相等（float64 结合顺序相同）。
// 形参 bs 即题面的块大小 b，避开矩阵参数名 b。
func Blocked(a, b []float64, n, bs int) []float64 {
	if !validInputs(a, b, n, bs) {
		return nil
	}
	parts := blk.Parts(n, bs) // 三维等长，复用同一份划分
	c := make([]float64, n*n)
	for _, pi := range parts { // i 块
		for _, pk := range parts { // k 块
			for _, pj := range parts { // j 块
				for i := pi.Start; i < pi.End; i++ { // 块内 i
					for k := pk.Start; k < pk.End; k++ { // 块内 k（升序）
						aik := a[i*n+k]
						for j := pj.Start; j < pj.End; j++ { // 块内 j
							c[i*n+j] += aik * b[k*n+j]
						}
					}
				}
			}
		}
	}
	return c
}
