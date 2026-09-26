// Package api 是对外门面：构造求解器、求最大特征值、内置自检。
package api

import (
	"fmt"

	"ontology/eigen"
)

// 对外再导出同一组哨兵错误，调用方用 errors.Is 判定。
var (
	ErrDimMismatch    = eigen.ErrDimMismatch
	ErrEmptySystem    = eigen.ErrEmptySystem
	ErrZeroVector     = eigen.ErrZeroVector
	ErrInvalidTol     = eigen.ErrInvalidTol
	ErrInvalidMaxIter = eigen.ErrInvalidMaxIter
	ErrNoConvergence  = eigen.ErrNoConvergence
)

// Solver 不可变，构造后可被多 goroutine 并发使用。
type Solver struct {
	tol     float64
	maxIter int
}

// New 校验 tol>0、maxIter>=1；非法参数整体失败，不产生任何状态。
func New(tol float64, maxIter int) (*Solver, error) {
	if !(tol > 0) {
		return nil, ErrInvalidTol
	}
	if maxIter < 1 {
		return nil, ErrInvalidMaxIter
	}
	return &Solver{tol: tol, maxIter: maxIter}, nil
}

// Eigen 返回模最大的特征值 λ 与归一化特征向量 v（max|v_i|==1）。
// a、v0 只读；失败时不留任何痕迹。
func (s *Solver) Eigen(a, v0 []float64, n int) (float64, []float64, error) {
	lam, v, _, err := eigen.Iterate(a, v0, n, s.tol, s.maxIter)
	if err != nil {
		return 0, nil, err
	}
	return lam, v, nil
}

// SelfCheck 用内置矩阵核验四条不变量：残差一致、归一化确定、
// 输入不被修改、失败不留痕。全部通过返回 nil，否则返回首个失败原因。
func (s *Solver) SelfCheck() error {
	cases := []struct {
		a   []float64
		v0  []float64
		n   int
		lam float64 // 已知的模最大特征值
	}{
		{[]float64{3, 1, 1, 3}, []float64{1, 0}, 2, 4},
		{[]float64{-3, 1, 1, -3}, []float64{1, 0}, 2, -4},
		{[]float64{5, 0, 0, 0, 0, 2, 0, 0, 0, 0, 1, 0, 0, 0, 0, -4}, []float64{1, 1, 1, 1}, 4, 5},
	}
	for i, c := range cases {
		a0, v00 := clone(c.a), clone(c.v0)
		lam, v, err := s.Eigen(c.a, c.v0, c.n)
		if err != nil {
			return fmt.Errorf("selfcheck case %d: %w", i, err)
		}
		av := matVec(c.a, v, c.n)
		for j := range av { // 不变量 1：A·v == λ·v，每项残差 ≤1e-9
			if d := av[j] - lam*v[j]; d > 1e-9 || d < -1e-9 {
				return fmt.Errorf("selfcheck case %d: residual %v", i, d)
			}
		}
		if lam != c.lam && abs(abs(lam)-abs(c.lam)) > 1e-6 { // λ 是模最大的
			return fmt.Errorf("selfcheck case %d: lam %v want |%v|", i, lam, c.lam)
		}
		lam2, v2, _ := s.Eigen(c.a, c.v0, c.n)
		if lam2 != lam { // 不变量 2：逐位一致
			return fmt.Errorf("selfcheck case %d: nondeterministic lam", i)
		}
		var mx float64
		for j, x := range v {
			if x != v2[j] {
				return fmt.Errorf("selfcheck case %d: nondeterministic v", i)
			}
			if ax := abs(x); ax > mx {
				mx = ax
			}
		}
		if mx != 1 { // 归一化：max|v_i| 恒为 1
			return fmt.Errorf("selfcheck case %d: max|v|=%v", i, mx)
		}
		if !equal(c.a, a0) || !equal(c.v0, v00) { // 不变量 3：输入不被修改
			return fmt.Errorf("selfcheck case %d: input mutated", i)
		}
	}
	// 不变量 4：各类拒绝互不相同、整体失败，且拒绝后仍可正常使用。
	bads := []error{
		callErr(s, []float64{1, 2, 3}, []float64{1, 0}, 2),    // 维度不一致
		callErr(s, nil, nil, 0),                               // 空系统
		callErr(s, []float64{1, 0, 0, 1}, []float64{0, 0}, 2), // 零起始向量
	}
	seen := map[error]bool{}
	for i, e := range bads {
		if e == nil || seen[e] {
			return fmt.Errorf("selfcheck: bad case %d not distinctly rejected", i)
		}
		seen[e] = true
	}
	if _, _, err := s.Eigen([]float64{3, 1, 1, 3}, []float64{1, 0}, 2); err != nil {
		return fmt.Errorf("selfcheck: unusable after rejection: %w", err)
	}
	return nil
}

func callErr(s *Solver, a, v0 []float64, n int) error {
	_, _, err := s.Eigen(a, v0, n)
	return err
}

func matVec(a, v []float64, n int) []float64 {
	w := make([]float64, n)
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			w[i] += a[i*n+j] * v[j]
		}
	}
	return w
}

func clone(x []float64) []float64 { return append([]float64(nil), x...) }

func equal(x, y []float64) bool {
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
