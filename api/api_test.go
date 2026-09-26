package api

import (
	"errors"
	"slices"
	"sync"
	"testing"
)

func fill(t *testing.T, m *Matcher, w [][]int64) {
	t.Helper()
	for i, row := range w {
		for j, x := range row {
			if err := m.SetWeight(i, j, x); err != nil {
				t.Fatalf("SetWeight(%d,%d): %v", i, j, err)
			}
		}
	}
}

func TestErrorsAreDistinct(t *testing.T) {
	errs := []error{ErrInvalidN, ErrNodeOutOfRange, ErrDuplicateWeight, ErrIncomplete}
	for i := range errs {
		for j := i + 1; j < len(errs); j++ {
			if errors.Is(errs[i], errs[j]) {
				t.Fatalf("哨兵错误 %v 与 %v 不互异", errs[i], errs[j])
			}
		}
	}
}

func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	if _, err := New(0); !errors.Is(err, ErrInvalidN) {
		t.Fatalf("New(0) err=%v", err)
	}
	if _, err := New(-3); !errors.Is(err, ErrInvalidN) {
		t.Fatalf("New(-3) err=%v", err)
	}
	m, err := New(3)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ l, r int }{
		{3, 0}, {-1, 0}, {0, 3}, {0, -1},
	} {
		if err := m.SetWeight(c.l, c.r, 1); !errors.Is(err, ErrNodeOutOfRange) {
			t.Fatalf("SetWeight(%d,%d) err=%v, want ErrNodeOutOfRange", c.l, c.r, err)
		}
	}
	if err := m.SetWeight(1, 1, 7); err != nil {
		t.Fatal(err)
	}
	if err := m.SetWeight(1, 1, 8); !errors.Is(err, ErrDuplicateWeight) {
		t.Fatalf("重复设置 err=%v, want ErrDuplicateWeight", err)
	}
	if _, _, err := m.Solve(); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("未设置完全 err=%v, want ErrIncomplete", err)
	}
	// 被拒操作不得改变已存权值。
	if w, ok := m.g.Weight(1, 1); !ok || w != 7 {
		t.Fatalf("被拒后 (1,1)=%d,%v，状态被污染", w, ok)
	}
	// 被拒后补全矩阵，对象仍可正常求解。
	w := [][]int64{{10, 8, 0}, {10, 7, 0}, {0, 10, 10}}
	for i := range w {
		for j := range w[i] {
			if !(i == 1 && j == 1) {
				if err := m.SetWeight(i, j, w[i][j]); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	v, match, err := m.Solve()
	if err != nil || v != 28 || !slices.Equal(match, []int{1, 0, 2}) {
		t.Fatalf("拒绝后求解异常: v=%d m=%v err=%v", v, match, err)
	}
}

func TestSelfCheck(t *testing.T) {
	m, err := New(3)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

func TestSolveSectionMatrix(t *testing.T) {
	m, _ := New(3)
	fill(t, m, [][]int64{{10, 8, 0}, {10, 0, 0}, {0, 10, 10}})
	v, match, err := m.Solve()
	if err != nil || v != 28 || !slices.Equal(match, []int{1, 0, 2}) {
		t.Fatalf("got (%d,%v,%v), want 28 [1 0 2]", v, match, err)
	}
}

// TestConcurrentSolve：N 个 goroutine 对同一冻结矩阵并发 Solve，
// 总权与匹配必须逐元素相同；不借助 sleep 制造时序。
func TestConcurrentSolve(t *testing.T) {
	m, _ := New(6)
	w := [][]int64{
		{3, 7, 13, -11, 5, 0},
		{-3, 19, 4, 13, -2, 8},
		{20, -16, -16, -8, 9, 11},
		{5, 4, 9, -20, 6, 2},
		{12, 1, -7, 14, 3, 18},
		{-4, 10, 2, 6, -13, 7},
	}
	fill(t, m, w)
	const N = 64
	var wg sync.WaitGroup
	vs := make([]int64, N)
	ms := make([][]int, N)
	wg.Add(N)
	for g := 0; g < N; g++ {
		go func(g int) {
			defer wg.Done()
			vs[g], ms[g], _ = m.Solve()
		}(g)
	}
	wg.Wait()
	for g := 1; g < N; g++ {
		if vs[g] != vs[0] || !slices.Equal(ms[g], ms[0]) {
			t.Fatalf("goroutine %d 结果 (%d,%v) != (%d,%v)", g, vs[g], ms[g], vs[0], ms[0])
		}
	}
}
