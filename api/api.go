// Package api 是幂迭代求最大特征值的对外入口，仅依赖 eigen 包。
// 状态仅为不可变的 tol/maxIter；Eigen 对输入只读且内部拷贝，可并发调用。
package api

import (
	"errors"
	"math"

	"ontology/eigen"
)

// Engine 持有求解参数；零值不可用，请用 New 构造。
type Engine struct {
	tol     float64
	maxIter int
}

// 透传哨兵错误，便于调用方 errors.Is 判定；四类故障互不相同。
var (
	ErrEmptySystem       = eigen.ErrEmptySystem
	ErrDimensionMismatch = eigen.ErrDimensionMismatch
	ErrZeroStartVector   = eigen.ErrZeroStartVector
	ErrNotConverged      = eigen.ErrNotConverged
	ErrNonPositiveTol    = eigen.ErrNonPositiveTol
	ErrBadMaxIter        = eigen.ErrBadMaxIter
)

// New 构造引擎；tol 必须 >0，maxIter 必须 ≥1，否则整体失败、不产生可用对象。
func New(tol float64, maxIter int) (*Engine, error) {
	if !(tol > 0) {
		return nil, ErrNonPositiveTol
	}
	if maxIter < 1 {
		return nil, ErrBadMaxIter
	}
	return &Engine{tol: tol, maxIter: maxIter}, nil
}

// Eigen 求 a 的模最大特征值 λ 与归一化特征向量 v（max|v_i|=1）。
// a、v0 全程只读：入口整体拷贝，调用前后入参逐字节不变。
func (e *Engine) Eigen(a, v0 []float64, n int) (float64, []float64, error) {
	ca := append([]float64(nil), a...)
	cv := append([]float64(nil), v0...)
	lam, v, _, err := eigen.Iterate(ca, cv, n, e.tol, e.maxIter)
	return lam, v, err
}

func bitsEq(x, y []float64) bool { // 逐位相等（含 ±0 区分）
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if math.Float64bits(x[i]) != math.Float64bits(y[i]) {
			return false
		}
	}
	return true
}

func residual(a, v []float64, n int, lam float64) float64 {
	r := 0.0
	for i := 0; i < n; i++ {
		var s float64
		for j := 0; j < n; j++ {
			s += a[i*n+j] * v[j]
		}
		if d := math.Abs(s - lam*v[i]); d > r {
			r = d
		}
	}
	return r
}

// SelfCheck 用内置矩阵核验第二节四条不变量，全部通过返回 nil。
func (e *Engine) SelfCheck() error {
	// 均为一轮即可精确收敛的对称矩阵（元素覆盖负、零、正），故残差严格为 0。
	cases := []struct {
		a    []float64
		n    int
		v0   []float64
		want float64
	}{
		{[]float64{1, 1, 1, 1}, 2, []float64{1, 0}, 2},
		{[]float64{-2, 2, 2, -2}, 2, []float64{0, 1}, -4},
		{[]float64{3, 0, -3, 0, 0, 0, -3, 0, 3}, 3, []float64{1, 0, 0}, 6},
	}
	for _, c := range cases {
		l1, v1, err := e.Eigen(c.a, c.v0, c.n)
		if err != nil || math.Abs(l1-c.want) > 1e-12 || residual(c.a, v1, c.n, l1) > 1e-9 {
			return errors.New("api: self-check invariant 1 failed: A*v == lambda*v and |lambda| maximal")
		}
		m := 0.0 // 不变量2：无穷范数恰为 1
		for _, x := range v1 {
			if math.Abs(x) > m {
				m = math.Abs(x)
			}
		}
		if math.Abs(m-1) > 1e-12 {
			return errors.New("api: self-check invariant 2 failed: inf norm")
		}
		l2, v2, err := e.Eigen(c.a, c.v0, c.n) // 同输入逐位可复现
		if err != nil || math.Float64bits(l1) != math.Float64bits(l2) || !bitsEq(v1, v2) {
			return errors.New("api: self-check invariant 2 failed: deterministic")
		}
	}

	// 不变量3：调用前后 a、v0 逐字节不变。
	a0, v0 := []float64{-2, 2, 2, -2}, []float64{0.0, 1}
	ca, cv := append([]float64(nil), a0...), append([]float64(nil), v0...)
	if _, _, err := e.Eigen(a0, v0, 2); err != nil || !bitsEq(a0, ca) || !bitsEq(v0, cv) {
		return errors.New("api: self-check invariant 3 failed: input mutated")
	}

	// 不变量4：拒绝操作哨兵互不相同；New 的非法参数同样可判定。
	if _, err := New(0, 1); !errors.Is(err, ErrNonPositiveTol) {
		return errors.New("api: self-check invariant 4 failed: tol")
	}
	if _, err := New(1e-9, 0); !errors.Is(err, ErrBadMaxIter) {
		return errors.New("api: self-check invariant 4 failed: maxIter")
	}
	rejects := []struct {
		a, v []float64
		n    int
		want error
	}{
		{make([]float64, 3), []float64{1, 0}, 2, ErrDimensionMismatch},
		{[]float64{1, 0, 0, 1}, []float64{}, 0, ErrEmptySystem},
		{[]float64{1, 0, 0, 1}, []float64{0, 0}, 2, ErrZeroStartVector},
	}
	for _, b := range rejects {
		if _, _, err := e.Eigen(b.a, b.v, b.n); !errors.Is(err, b.want) {
			return errors.New("api: self-check invariant 4 failed: sentinel")
		}
	}
	strict, _ := New(1e-30, 1) // 一轮内 |3.6-0| 远大于 tol → 不收敛
	if _, _, err := strict.Eigen([]float64{3, 1, 1, 3}, []float64{1, 0}, 2); !errors.Is(err, ErrNotConverged) {
		return errors.New("api: self-check invariant 4 failed: not converged")
	}
	if _, _, err := e.Eigen(cases[0].a, cases[0].v0, 2); err != nil { // 被拒后引擎仍正常
		return errors.New("api: self-check invariant 4 failed: engine unusable after reject")
	}
	return nil
}
