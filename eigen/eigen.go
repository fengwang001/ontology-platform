// Package eigen 驱动幂迭代：w=A·v、无穷范数归一化、归一化后向量的 Rayleigh 商，
// 以 |λ_k-λ_{k-1}|（λ0=0）为收敛判据。仅依赖 power 包。
package eigen

import (
	"errors"
	"math"
	"sync/atomic"

	"ontology/power"
)

// 可判定的哨兵错误，彼此互不相同。
var (
	ErrEmptySystem       = errors.New("eigen: empty system: n must be >= 1")
	ErrDimensionMismatch = errors.New("eigen: dimension mismatch: len(a) must be n*n and len(v0) must be n")
	ErrZeroStartVector   = errors.New("eigen: zero start vector: v0 must be nonzero")
	ErrNotConverged      = errors.New("eigen: not converged within maxIter iterations")
	ErrNonPositiveTol    = errors.New("eigen: tol must be > 0")
	ErrBadMaxIter        = errors.New("eigen: maxIter must be >= 1")
)

// matVecOps 是非导出计数器：实际执行的矩阵-向量乘法次数。
// 它不出现在任何公开接口里，仅本包白盒测试可访问；用原子操作支持并发。
var matVecOps atomic.Int64

func countedMatVec(a, v []float64, n int) []float64 {
	matVecOps.Add(1)
	return power.MatVec(a, v, n)
}

// Iterate 对 n×n 对称矩阵 a 求模最大特征值及特征向量。
// 每轮：w=A·v，v'=w/infnorm(w)，λ=(v'ᵀ·A·v')/(v'ᵀv')（复用下一轮滚动的 A·v'，每轮仅一次 matvec）。
// 返回的 v 无穷范数恒为 1；a 全程只读，v0 内部拷贝后使用。
func Iterate(a, v0 []float64, n int, tol float64, maxIter int) (float64, []float64, int, error) {
	// 所有校验先于任何状态变更（计数器、分配外的可观察状态）。
	if n < 1 {
		return 0, nil, 0, ErrEmptySystem
	}
	if len(a) != n*n || len(v0) != n {
		return 0, nil, 0, ErrDimensionMismatch
	}
	if !(tol > 0) { // 同时拒绝 NaN
		return 0, nil, 0, ErrNonPositiveTol
	}
	if maxIter < 1 {
		return 0, nil, 0, ErrBadMaxIter
	}
	v := append([]float64(nil), v0...) // 拷贝起始向量，绝不修改入参
	if power.InfNorm(v) == 0 {
		return 0, nil, 0, ErrZeroStartVector
	}

	w := countedMatVec(a, v, n) // A·v0 启动乘法
	done := int64(1)
	prev := 0.0 // λ0 视为 0
	for k := 1; k <= maxIter; k++ {
		m := power.InfNorm(w)
		if m == 0 { // 起始向量落入零空间，归一化因子为 0，按规则无法继续
			matVecOps.Add(-done)
			return 0, nil, 0, ErrNotConverged
		}
		vk := make([]float64, n)
		for i := range w { // 归一化：绝对值最大分量恰为 ±1，符号保留
			vk[i] = w[i] / m
		}
		wk := countedMatVec(a, vk, n) // A·v_k，本轮唯一 matvec，亦用于 Rayleigh 商
		done++
		var num, den float64
		for i := range vk {
			num += vk[i] * wk[i]
			den += vk[i] * vk[i]
		}
		lam := num / den
		if math.Abs(lam-prev) < tol {
			return lam, vk, k, nil
		}
		prev = lam
		w = wk
	}
	// 不收敛：精确抵消本次调用产生的计数（Add 而非 Store，并发下不波及他人）。
	matVecOps.Add(-done)
	return 0, nil, 0, ErrNotConverged
}
