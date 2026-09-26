// Package api 差分约束系统对外接口。依赖 sp；求解类方法并发安全。
package api

import (
	"errors"
	"fmt"

	"ontology/dc"
	"ontology/sp"
)

// 四类可判定哨兵错误，互不相同。
var (
	ErrNonPositiveN     = dc.ErrNonPositiveN     // n 非正
	ErrVarOutOfRange    = dc.ErrVarOutOfRange    // 约束引用 [0,n) 外变量
	ErrNegativeSelfLoop = dc.ErrNegativeSelfLoop // 自环负权
	ErrInfeasible       = sp.ErrInfeasible       // 负环，系统不可行
)

// System 一组差分约束。约束登记完成后，Solve/ConstraintCount/SelfCheck 可并发调用。
type System struct{ reg *dc.Registry }

// New 固定变量数 n；n 非正返回 ErrNonPositiveN。
func New(n int) (*System, error) {
	r, err := dc.New(n)
	if err != nil {
		return nil, err
	}
	return &System{reg: r}, nil
}

// AddConstraint 登记 x_v-x_u<=w；非法约束被拒绝且不改变已登记集合。
func (s *System) AddConstraint(u, v int, w int64) error { return s.reg.Add(u, v, w) }

// ConstraintCount 已登记约束条数。
func (s *System) ConstraintCount() int { return s.reg.Count() }

// Solve 返回点态最大可行赋值；系统含负环时返回 (nil, ErrInfeasible)。
func (s *System) Solve() ([]int64, error) { return sp.Solve(s.reg.N(), s.reg.Constraints()) }

// naiveBF 朴素参照：超级源为起点（源边权 0 ⇒ 初始距离全 0），逐轮全量松弛所有边直到无变化。
func naiveBF(n int, cs []dc.Constraint) ([]int64, error) {
	dist := make([]int64, n)
	for round := 0; ; round++ {
		changed := false
		for _, c := range cs {
			if d := dist[c.U] + c.W; d < dist[c.V] {
				dist[c.V] = d
				changed = true
			}
		}
		if !changed {
			return dist, nil
		}
		if round >= n { // 已松 n 轮仍变化 ⇒ 负环
			return nil, ErrInfeasible
		}
	}
}

func equal(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// tighten 加参考变量 p=n：边 p->j 权 0（x_j<=x_p）与边 i->p 权 -(got_i+1)（x_i>=x_p+got_i+1）。
// 差分约束平移不变，x_p 可视为 0：收紧系统不可行 ⟺ 原系统不存在 y_i>=got_i+1 的可行解。
func tighten(n int, cs []dc.Constraint, i int, got []int64) []dc.Constraint {
	out := append([]dc.Constraint{}, cs...)
	for j := 0; j < n; j++ {
		out = append(out, dc.Constraint{U: n, V: j, W: 0})
	}
	return append(out, dc.Constraint{U: i, V: n, W: -got[i] - 1})
}

// SelfCheck 对内置约束序列核验四条不变量，全部通过返回 nil。
func (s *System) SelfCheck() error {
	const n = 4
	cs := []dc.Constraint{{U: 0, V: 1, W: -2}, {U: 1, V: 2, W: -3}, {U: 0, V: 3, W: 1}, {U: 2, V: 3, W: 0}, {U: 3, V: 1, W: 10}}
	got, err := sp.Solve(n, cs)
	if err != nil {
		return fmt.Errorf("selfcheck solve: %w", err)
	}
	for _, c := range cs { // 不变量1：可行
		if got[c.V]-got[c.U] > c.W {
			return fmt.Errorf("selfcheck: 约束 %v 被违反", c)
		}
	}
	want, err := naiveBF(n, cs) // 不变量3：与朴素参照一致
	if err != nil || !equal(got, want) {
		return fmt.Errorf("selfcheck: 与朴素 Bellman-Ford 不一致")
	}
	for i := 0; i < n; i++ { // 不变量2：点态最大，任一变量加严 1 即不可行
		if _, err := sp.Solve(n+1, tighten(n, cs, i, got)); !errors.Is(err, ErrInfeasible) {
			return fmt.Errorf("selfcheck: x%d 非点态最大", i)
		}
	}
	before := s.ConstraintCount() // 不变量4：失败不留痕
	bad := []error{
		s.AddConstraint(-1, 0, 0), s.AddConstraint(0, s.reg.N(), 0), s.AddConstraint(0, 0, -1),
	}
	for _, e := range bad {
		if e == nil {
			return errors.New("selfcheck: 非法约束被接受")
		}
	}
	if _, err := sp.Solve(2, []dc.Constraint{{U: 0, V: 1, W: -1}, {U: 1, V: 0, W: -1}}); !errors.Is(err, ErrInfeasible) {
		return errors.New("selfcheck: 负环未检出")
	}
	if s.ConstraintCount() != before {
		return errors.New("selfcheck: 被拒操作改变了状态")
	}
	return nil
}
