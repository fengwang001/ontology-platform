package view

import (
	"fmt"
	"testing"
)

func mustAdd(t *testing.T, s *Store, n string, d []string, f func(...int64) int64) {
	t.Helper()
	if err := s.Add(n, d, f); err != nil {
		t.Fatal(err)
	}
}

func buildChain(t *testing.T) *Store {
	t.Helper()
	s := New()
	mustAdd(t, s, "A", nil, nil)
	mustAdd(t, s, "B", nil, nil)
	mustAdd(t, s, "E", []string{"C", "D"}, func(a ...int64) int64 { return a[0] + a[1] })
	mustAdd(t, s, "F", []string{"D"}, func(a ...int64) int64 { return a[0] - 1 })
	mustAdd(t, s, "C", []string{"A", "B"}, func(a ...int64) int64 { return a[0] + a[1] })
	mustAdd(t, s, "D", []string{"A"}, func(a ...int64) int64 { return a[0] * 2 })
	return s
}

// TestSection3：逐步对拍 NOTES.md 推导表（含前向引用注册序）。
func TestSection3(t *testing.T) {
	s := buildChain(t)
	s.Set("A", 1)
	s.Set("B", 2)
	_ = s.Recompute()
	want := map[string]int64{"A": 1, "B": 2, "C": 3, "D": 2, "E": 5, "F": 1}
	s.Set("B", 20)
	_ = s.Recompute()
	for n, v := range map[string]int64{"B": 20, "C": 21, "E": 23} {
		want[n] = v
	}
	s.Set("A", 10)
	_ = s.Recompute()
	for n, v := range map[string]int64{"A": 10, "C": 30, "D": 20, "E": 50, "F": 19} {
		want[n] = v
	}
	for n, w := range want {
		if got, _ := s.Get(n); got != w {
			t.Fatalf("%s=%d want %d", n, got, w)
		}
	}
}

// TestTopologicalFreshness：求值某视图时读到的必须是依赖的最新值。
func TestTopologicalFreshness(t *testing.T) {
	s := New()
	var seenC int64
	mustAdd(t, s, "A", nil, nil)
	mustAdd(t, s, "C", []string{"A"}, func(a ...int64) int64 { return a[0] * 10 })
	mustAdd(t, s, "E", []string{"C"}, func(a ...int64) int64 { seenC = a[0]; return a[0] })
	s.Set("A", 7)
	if err := s.Recompute(); err != nil {
		t.Fatal(err)
	}
	if seenC != 70 {
		t.Fatalf("E saw C=%d want 70 (fresh)", seenC)
	}
}

// TestDedupSingleEval：同一轮内被多处失效的视图只求值一次；干净视图零求值。
func TestDedupSingleEval(t *testing.T) {
	s := New()
	mustAdd(t, s, "A", nil, nil)
	mustAdd(t, s, "B", nil, nil)
	mustAdd(t, s, "C", []string{"A", "B"}, func(a ...int64) int64 { return a[0] + a[1] })
	mustAdd(t, s, "D", []string{"A"}, func(a ...int64) int64 { return a[0] })
	s.Set("A", 1) // C、D 均被失效
	s.Set("B", 2) // C 再次被失效
	if err := s.Recompute(); err != nil {
		t.Fatal(err)
	}
	if s.last != 2 {
		t.Fatalf("evaluated %d, want 2 (C and D once each)", s.last)
	}
	if err := s.Recompute(); err != nil || s.last != 0 {
		t.Fatalf("clean recompute: last=%d err=%v, want 0", s.last, err)
	}
}

// TestEvalCountIndependentOfM：仅 Set(X) 后求值个数不随独立基视图数 m 增长。
func TestEvalCountIndependentOfM(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		s := New()
		for i := 0; i < m; i++ {
			mustAdd(t, s, fmt.Sprintf("W%d", i), nil, nil)
		}
		mustAdd(t, s, "X", nil, nil)
		mustAdd(t, s, "Y", []string{"X"}, func(a ...int64) int64 { return a[0] + 1 })
		mustAdd(t, s, "Z", []string{"Y"}, func(a ...int64) int64 { return a[0] + 1 })
		s.Set("X", 1)
		if err := s.Recompute(); err != nil {
			t.Fatal(err)
		}
		if s.last != 2 {
			t.Fatalf("m=%d: evaluated %d views, want 2 (only Y,Z)", m, s.last)
		}
		if v, _ := s.Get("Z"); v != 3 {
			t.Fatalf("m=%d: Z=%d want 3", m, v)
		}
	}
}

// TestUnresolvedAndCycle：未解析依赖与环都可判定且状态不变。
func TestUnresolvedAndCycle(t *testing.T) {
	s := New()
	mustAdd(t, s, "P", []string{"Q"}, func(a ...int64) int64 { return a[0] })
	if err := s.Recompute(); err != ErrUnresolved {
		t.Fatalf("err=%v want ErrUnresolved", err)
	}
	mustAdd(t, s, "M", []string{"N"}, nil)
	if err := s.Add("N", []string{"M"}, nil); err == nil {
		t.Fatal("cycle not rejected")
	}
	if s.Has("N") {
		t.Fatal("rejected Add changed state")
	}
}
