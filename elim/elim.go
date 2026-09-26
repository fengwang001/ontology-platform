// Package elim 用带部分主元的高斯消元求方阵行列式。
package elim

import (
	"errors"
	"math"
	"sync/atomic"

	"ontology/pivot"
)

// 三类可判定、互不相同的哨兵错误。
var (
	ErrEmpty     = errors.New("elim: empty matrix (n < 1)")
	ErrDimension = errors.New("elim: dimension mismatch (len(a) != n*n)")
	ErrNonFinite = errors.New("elim: matrix contains NaN or Inf")
)

// Engine 持有消元过程的内部状态。零值不可用，用 New 构造。
type Engine struct {
	// ops 记录实际执行的乘减行操作次数（主元扫描不计）。
	// 非导出：只能由同包白盒测试读取。
	ops atomic.Int64
}

func New() *Engine { return &Engine{} }

// Det 用部分主元高斯消元求 n×n 行主序矩阵 a 的行列式。
// 输入只读：内部复制后运算。奇异矩阵返回 (0, nil)。
func (e *Engine) Det(a []float64, n int) (float64, error) {
	// 全部校验在任何状态变更之前完成：失败不留痕。
	if n < 1 {
		return 0, ErrEmpty
	}
	if len(a) != n*n {
		return 0, ErrDimension
	}
	for _, v := range a {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return 0, ErrNonFinite
		}
	}

	m := append([]float64(nil), a...) // 整体复制，此后只碰副本
	sign := 1.0
	for k := 0; k < n; k++ {
		r, ok := pivot.Pick(m, n, k)
		if !ok {
			return 0, nil // 主元列全零 => 奇异
		}
		if r != k {
			for j := 0; j < n; j++ {
				m[k*n+j], m[r*n+j] = m[r*n+j], m[k*n+j]
			}
			sign = -sign
		}
		piv := m[k*n+k]
		for i := k + 1; i < n; i++ {
			if m[i*n+k] == 0 {
				continue // 已消好的行：跳过乘减，不计操作数
			}
			f := m[i*n+k] / piv
			for j := k; j < n; j++ {
				m[i*n+j] -= f * m[k*n+j]
			}
			e.ops.Add(1)
		}
	}
	det := sign
	for k := 0; k < n; k++ {
		det *= m[k*n+k]
	}
	return det, nil
}
