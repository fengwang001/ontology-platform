// Package api 对外暴露线性方程组求解能力。
package api

import (
	"errors"
	"math"

	"ontology/elim"
)

// Solver 无内部状态，可并发使用。
type Solver struct{}

// New 返回一个可并发安全使用的 Solver。
func New() *Solver { return &Solver{} }

// Solve 解 n×n 方程组 Ax=b，不修改输入。
func (s *Solver) Solve(a, b []float64, n int) ([]float64, error) {
	return elim.Solve(a, b, n)
}

// SelfCheck 对内置方程组核验四条不变量，全部通过返回 nil。
func (s *Solver) SelfCheck() error {
	// 内置非奇异方程组：A=[[0,1,1],[1,0,1],[1,1,0]]，b=[5,4,3]，解 [1,2,3]。
	a := []float64{0, 1, 1, 1, 0, 1, 1, 1, 0}
	b := []float64{5, 4, 3}
	aCopy, bCopy := append([]float64(nil), a...), append([]float64(nil), b...)

	x, err := s.Solve(a, b, 3)
	if err != nil {
		return err
	}
	// 不变量1：残差 |A·x - b| 每项 ≤1e-9。
	for i := 0; i < 3; i++ {
		r := -bCopy[i]
		for j := 0; j < 3; j++ {
			r += aCopy[i*3+j] * x[j]
		}
		if math.Abs(r) > 1e-9 {
			return errors.New("api: residual exceeds 1e-9")
		}
	}
	// 不变量2：输入不被修改。
	for i := range a {
		if a[i] != aCopy[i] {
			return errors.New("api: input a mutated")
		}
	}
	for i := range b {
		if b[i] != bCopy[i] {
			return errors.New("api: input b mutated")
		}
	}
	// 不变量3：主元选择确定——同一输入重复求解结果逐字节一致。
	x2, err := s.Solve(a, b, 3)
	if err != nil {
		return err
	}
	for i := range x {
		if x[i] != x2[i] {
			return errors.New("api: nondeterministic result")
		}
	}
	// 不变量4：失败不留痕——三类拒绝（空系统/维度不一致/奇异）后仍可正常求解。
	singular := []float64{1, 0, 0, 0, 0, 0, 0, 0, 0}
	_, _ = s.Solve(a[:0], b[:0], 0) // ErrEmpty
	_, _ = s.Solve(a, b[:2], 3)     // ErrDimension
	_, _ = s.Solve(singular, b, 3)  // ErrSingular
	if _, err := s.Solve(a, b, 3); err != nil {
		return errors.New("api: state corrupted after rejection")
	}
	return nil
}
