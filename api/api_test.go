package api

import (
	"errors"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/dc"
	"ontology/sp"
)

// 表驱动：求解结果须等于期望赋值；不可行用例须报 ErrInfeasible。
func TestSolveTable(t *testing.T) {
	cases := []struct {
		name string
		n    int
		cs   []dc.Constraint
		want []int64 // nil 表示不可行
	}{
		{"第三节C1C2", 3, []dc.Constraint{{U: 0, V: 1, W: -2}, {U: 1, V: 2, W: -3}}, []int64{0, -2, -5}},
		{"负边不成环", 2, []dc.Constraint{{U: 0, V: 1, W: -7}}, []int64{0, -7}},
		{"C3负环", 3, []dc.Constraint{{U: 0, V: 1, W: -2}, {U: 1, V: 2, W: -3}, {U: 2, V: 0, W: -10}}, nil},
	}
	for _, c := range cases {
		s, _ := New(c.n)
		for _, k := range c.cs {
			if err := s.AddConstraint(k.U, k.V, k.W); err != nil {
				t.Fatalf("%s: Add: %v", c.name, err)
			}
		}
		got, err := s.Solve()
		if c.want == nil {
			if !errors.Is(err, ErrInfeasible) {
				t.Errorf("%s: 应报 ErrInfeasible, 得到 %v", c.name, err)
			}
			continue
		}
		if err != nil || !equal(got, c.want) {
			t.Errorf("%s: got %v, want %v (err %v)", c.name, got, c.want, err)
		}
	}
}

// 随机可行系统（由目标解 y 生成、随机顺序）：解满足全部约束、y<=x 点态成立、与朴素参照一致。
func TestRandomConsistent(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for _, n := range []int{2, 5, 17, 50} {
		for trial := 0; trial < 20; trial++ {
			y := make([]int64, n)
			for i := range y {
				y[i] = int64(-rng.Intn(50))
			}
			var cs []dc.Constraint
			for k := 0; k < 3*n; k++ {
				u, v := rng.Intn(n), rng.Intn(n)
				if w := y[v] - y[u] + int64(rng.Intn(5)); u != v || w >= 0 {
					cs = append(cs, dc.Constraint{U: u, V: v, W: w})
				}
			}
			got, err := sp.Solve(n, cs)
			if err != nil {
				t.Fatalf("n=%d: %v", n, err)
			}
			ref, _ := naiveBF(n, cs)
			for i := 0; i < n; i++ {
				if got[i] != ref[i] || y[i] > got[i] || got[i] > 0 {
					t.Fatalf("n=%d x%d: got %d, ref %d, y %d", n, i, got[i], ref[i], y[i])
				}
			}
			for _, c := range cs {
				if got[c.V]-got[c.U] > c.W {
					t.Fatalf("n=%d: 约束 %v 被违反", n, c)
				}
			}
		}
	}
}

// 点态最大：收紧任一变量 1 即不可行。
func TestPointwiseMax(t *testing.T) {
	cs := []dc.Constraint{{U: 0, V: 1, W: -2}, {U: 1, V: 2, W: -3}, {U: 0, V: 3, W: 1}, {U: 2, V: 3, W: 0}, {U: 3, V: 1, W: 10}}
	got, err := sp.Solve(4, cs)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if _, err := sp.Solve(5, tighten(4, cs, i, got)); !errors.Is(err, ErrInfeasible) {
			t.Errorf("x%d 收紧 1 后应不可行", i)
		}
	}
}

// 四类哨兵错误互不相同；被拒操作不改变状态，系统仍可正常使用；SelfCheck 通过。
func TestErrorsStateIntact(t *testing.T) {
	e4 := []error{ErrNonPositiveN, ErrVarOutOfRange, ErrNegativeSelfLoop, ErrInfeasible}
	for i, a := range e4 {
		for _, b := range e4[i+1:] {
			if errors.Is(a, b) {
				t.Fatalf("哨兵错误 %v 与 %v 未区分", a, b)
			}
		}
	}
	if _, err := New(0); !errors.Is(err, ErrNonPositiveN) {
		t.Errorf("New(0) 应报 ErrNonPositiveN")
	}
	s, _ := New(3)
	_ = s.AddConstraint(0, 1, -2)
	for _, a := range [][3]int64{{-1, 0, 0}, {0, 3, 0}, {1, 1, -1}} {
		if err := s.AddConstraint(int(a[0]), int(a[1]), a[2]); err == nil {
			t.Errorf("非法约束 %v 被接受", a)
		}
	}
	if s.ConstraintCount() != 1 {
		t.Errorf("被拒后 ConstraintCount 改变")
	}
	if got, err := s.Solve(); err != nil || got[1] != -2 {
		t.Errorf("被拒后求解异常: %v %v", got, err)
	}
	if err := s.SelfCheck(); err != nil {
		t.Errorf("SelfCheck: %v", err)
	}
}

// 并发：N 个 goroutine 对同一已构建约束集并发 Solve，结果逐元素相同。不用 sleep。
func TestConcurrentSolve(t *testing.T) {
	s, _ := New(4)
	for _, c := range [][3]int64{{0, 1, -2}, {1, 2, -3}, {0, 3, 1}, {2, 3, 0}} {
		_ = s.AddConstraint(int(c[0]), int(c[1]), c[2])
	}
	want, _ := s.Solve()
	var wg sync.WaitGroup
	var bad atomic.Bool
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for it := 0; it < 100; it++ {
				if got, err := s.Solve(); err != nil || !equal(got, want) {
					bad.Store(true)
				}
			}
		}()
	}
	wg.Wait()
	if bad.Load() {
		t.Fatal("并发 Solve 结果不一致")
	}
}
