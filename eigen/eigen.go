// Package eigen 驱动幂迭代：每轮 w=A·v，按带符号绝对值最大分量归一化，
// λ 取归一化后向量的 Rayleigh 商，收敛到模最大的特征值。
package eigen

import (
	"errors"
	"math"
	"sync/atomic"

	"ontology/power"
)

// 可判定的哨兵错误，互不相同。
var (
	ErrDimMismatch    = errors.New("eigen: dimension mismatch")
	ErrEmptySystem    = errors.New("eigen: empty system")
	ErrZeroVector     = errors.New("eigen: zero start vector")
	ErrInvalidTol     = errors.New("eigen: tol must be > 0")
	ErrInvalidMaxIter = errors.New("eigen: maxIter must be >= 1")
	ErrNoConvergence  = errors.New("eigen: not converged within maxIter")
)

// matVecCalls 记录实际矩阵-向量乘法次数；非导出、原子保护（白盒测试同包访问）。
var matVecCalls atomic.Int64

// Iterate 从非零 v0 做幂迭代，|λ_k−λ_{k−1}|<tol（λ_0 视为 0）即收敛。
// 返回 λ、归一化特征向量 v（max|v_i|==1）与实际矩阵-向量乘法次数。
// 输入只读；任何拒绝不触碰任何状态（含计数器），整体失败。
func Iterate(a, v0 []float64, n int, tol float64, maxIter int) (lam float64, v []float64, iters int, err error) {
	before := matVecCalls.Load()
	defer func() { iters = int(matVecCalls.Load() - before) }() // iters 恒等于真实乘法次数

	switch {
	case n < 1:
		return 0, nil, 0, ErrEmptySystem
	case len(a) != n*n || len(v0) != n:
		return 0, nil, 0, ErrDimMismatch
	case !(tol > 0):
		return 0, nil, 0, ErrInvalidTol
	case maxIter < 1:
		return 0, nil, 0, ErrInvalidMaxIter
	}
	zero := true
	for _, x := range v0 {
		zero = zero && x == 0
	}
	if zero {
		return 0, nil, 0, ErrZeroVector
	}

	// 自举：v1=(A·v0) 归一化并备妥 A·v1，此后每轮只新增一次乘法。
	w := matVec(a, v0, n)
	m := signedMax(w)
	if m == 0 {
		return 0, nil, 0, ErrNoConvergence // v0 落在零空间，迭代坍缩
	}
	v = scale(w, m)
	w = matVec(a, v, n)

	prev := 0.0 // λ_0 视为 0
	for k := 1; k <= maxIter; k++ {
		lam = dot(v, w) / dot(v, v) // Rayleigh 商，w = A·v
		if math.Abs(lam-prev) < tol {
			// 抛光：λ 以 ε² 收敛而 v 只以 ε 收敛，|Δλ|<tol 时残差约 √tol；
			// 继续同一规则至残差 ≤1e-9，不变量 1 才成立。
			v, lam = polish(a, v, w, n)
			return lam, v, 0, nil
		}
		prev = lam
		if m = signedMax(w); m == 0 {
			return 0, nil, 0, ErrNoConvergence // 迭代坍缩到零向量
		}
		v = scale(w, m)
		w = matVec(a, v, n)
	}
	return 0, nil, 0, ErrNoConvergence
}

// polish 继续同一迭代规则至残差 ≤1e-9 或 v 逐位稳定（浮点地板）。
func polish(a, v, w []float64, n int) ([]float64, float64) {
	for p := 0; p < 100000; p++ {
		lam := dot(v, w) / dot(v, v) // w = A·v 已备好，查残差不花乘法
		var res float64
		for i := range v {
			res = math.Max(res, math.Abs(w[i]-lam*v[i]))
		}
		if res <= 1e-9 {
			return v, lam
		}
		m := signedMax(w)
		if m == 0 {
			return v, lam
		}
		nv := scale(w, m)
		if equalVec(nv, v) {
			return nv, lam // 已到浮点地板
		}
		v = nv
		w = matVec(a, v, n)
	}
	return v, dot(v, w) / dot(v, v)
}

// matVec 包一层计数：每次调用即一次真实矩阵-向量乘法。
func matVec(a, v []float64, n int) []float64 {
	matVecCalls.Add(1)
	return power.MatVec(a, v, n)
}

// signedMax 返回首个绝对值最大分量的带符号值；全零返回 0。
func signedMax(w []float64) float64 {
	mi := 0
	for i := 1; i < len(w); i++ {
		if math.Abs(w[i]) > math.Abs(w[mi]) {
			mi = i
		}
	}
	return w[mi]
}

// scale 返回新切片 w/m。
func scale(w []float64, m float64) []float64 {
	v := make([]float64, len(w))
	for i, x := range w {
		v[i] = x / m
	}
	return v
}

func equalVec(x, y []float64) bool {
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

func dot(x, y []float64) float64 {
	var s float64
	for i := range x {
		s += x[i] * y[i]
	}
	return s
}
