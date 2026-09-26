package sp

import (
	"errors"
	"testing"

	"ontology/dc"
)

func chain(m int) []dc.Constraint {
	cs := make([]dc.Constraint, 0, m-1)
	for i := 0; i+1 < m; i++ {
		cs = append(cs, dc.Constraint{U: i, V: i + 1, W: -1})
	}
	return cs
}

// 长链 x_{i+1}-x_i<=-1：队列驱动只做 O(m) 次局部松弛，整表全量松弛趟数恒 0（朴素 BF 需 m 趟）。
func TestZeroSweepsLongChain(t *testing.T) {
	for _, m := range []int{100, 500, 2000, 10000} {
		s := NewSolver(m, chain(m))
		got, err := s.run()
		if err != nil {
			t.Fatalf("m=%d: %v", m, err)
		}
		if s.sweeps != 0 {
			t.Errorf("m=%d: 全量松弛趟数=%d, 应恒 0", m, s.sweeps)
		}
		if s.ops > 2*m {
			t.Errorf("m=%d: 局部松弛次数=%d, 超出 O(m)", m, s.ops)
		}
		for i := 0; i < m; i++ {
			if got[i] != int64(-i) {
				t.Fatalf("m=%d: x%d=%d, 应为 %d", m, i, got[i], -i)
			}
		}
	}
}

// 负环判定：表驱动，含负环必报 ErrInfeasible，可行系统必出可行解。
func TestNegativeCycleTable(t *testing.T) {
	cases := []struct {
		name string
		n    int
		cs   []dc.Constraint
		bad  bool
	}{
		{"两点互压", 2, []dc.Constraint{{U: 0, V: 1, W: -1}, {U: 1, V: 0, W: -1}}, true},
		{"三节负环", 3, []dc.Constraint{{U: 0, V: 1, W: -2}, {U: 1, V: 2, W: -3}, {U: 2, V: 0, W: -10}}, true},
		{"零环可行", 2, []dc.Constraint{{U: 0, V: 1, W: -1}, {U: 1, V: 0, W: 1}}, false},
		{"负边不成环", 3, []dc.Constraint{{U: 0, V: 1, W: -2}, {U: 1, V: 2, W: -3}}, false},
	}
	for _, c := range cases {
		got, err := Solve(c.n, c.cs)
		if c.bad && !errors.Is(err, ErrInfeasible) {
			t.Errorf("%s: 应报 ErrInfeasible, 得到 %v", c.name, err)
		}
		if !c.bad && err != nil {
			t.Errorf("%s: 不应报错, 得到 %v", c.name, err)
		}
		if err == nil {
			for _, e := range c.cs {
				if got[e.V]-got[e.U] > e.W {
					t.Errorf("%s: 约束 %v 被违反", c.name, e)
				}
			}
		}
	}
}
