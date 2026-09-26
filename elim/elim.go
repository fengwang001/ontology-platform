// Package elim 实现部分主元高斯消元与回代。
package elim

import (
	"errors"

	"ontology/pivot"
)

// 可判定的哨兵错误，三者互不相同。
var (
	ErrEmpty     = errors.New("elim: empty system (n < 1)")
	ErrDimension = errors.New("elim: dimension mismatch (len(a)!=n*n or len(b)!=n)")
	ErrSingular  = errors.New("elim: singular matrix")
)

// Solve 解 n×n 线性方程组 Ax=b。不修改输入 a、b（内部复制后运算）。
func Solve(a, b []float64, n int) ([]float64, error) {
	if n < 1 {
		return nil, ErrEmpty
	}
	if len(a) != n*n || len(b) != n {
		return nil, ErrDimension
	}
	// 全部校验通过后才复制，失败不留痕。
	ac := make([]float64, n*n)
	copy(ac, a)
	bc := make([]float64, n)
	copy(bc, b)

	// 前向消元：部分主元，交换同时作用于 ac 与 bc。
	for k := 0; k < n; k++ {
		r, ok := pivot.Pick(ac, n, k)
		if !ok {
			return nil, ErrSingular
		}
		if r != k {
			for j := k; j < n; j++ {
				ac[k*n+j], ac[r*n+j] = ac[r*n+j], ac[k*n+j]
			}
			bc[k], bc[r] = bc[r], bc[k]
		}
		for i := k + 1; i < n; i++ {
			m := ac[i*n+k] / ac[k*n+k]
			if m == 0 {
				continue
			}
			for j := k; j < n; j++ {
				ac[i*n+j] -= m * ac[k*n+j]
			}
			bc[i] -= m * bc[k]
		}
	}

	// 回代：从 x[n-1] 逆序到 x[0]。
	x := make([]float64, n)
	for i := n - 1; i >= 0; i-- {
		s := bc[i]
		for j := i + 1; j < n; j++ {
			s -= ac[i*n+j] * x[j]
		}
		x[i] = s / ac[i*n+i]
	}
	return x, nil
}
