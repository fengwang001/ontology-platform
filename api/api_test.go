package api

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
)

// 不变量 1：九步序列逐步 Get 与第三节推导的批量结果一致（表驱动）。
func TestNineStepTrace(t *testing.T) {
	s := New(1000)
	steps := []struct {
		op   func() error
		want map[string]int // 该步之后各键 Get 期望值
	}{
		{func() error { return s.Put("a", 3) }, map[string]int{"a": 3}},
		{func() error { return s.Put("b", 5) }, map[string]int{"a": 3, "b": 5}},
		{s.BeginMigration, map[string]int{"a": 3, "b": 5}},
		{func() error { return s.Put("c", 4) }, map[string]int{"a": 3, "b": 5, "c": 4}},
		{s.Backfill, map[string]int{"a": 3, "b": 5, "c": 4}},
		{s.Backfill, map[string]int{"a": 3, "b": 5, "c": 4}},
		{func() error { return s.Put("a", 7) }, map[string]int{"a": 7, "b": 5, "c": 4}},
		{s.Switch, map[string]int{"a": 14, "b": 10, "c": 8}},
		{func() error { return s.Put("d", 2) }, map[string]int{"a": 14, "b": 10, "c": 8, "d": 4}},
	}
	for i, st := range steps {
		st.op()
		for k, want := range st.want {
			if got := s.Get(k); got != want {
				t.Fatalf("step %d Get(%q)=%d, want %d", i+1, k, got, want)
			}
		}
	}
}

// 不变量 4：被拒操作不改变任何状态（表驱动）。
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*Store) error
		op    func(*Store) error
		want  error
		fault bool // 故障仍在：后续 Put 依旧被拒，改用 Get 验证可用
	}{
		{"switch-in-normal", nil, (*Store).Switch, ErrPhase, false},
		{"backfill-in-normal", nil, (*Store).Backfill, ErrPhase, false},
		{"begin-twice", (*Store).BeginMigration, (*Store).BeginMigration, ErrPhase, false},
		{"empty-key", nil, func(s *Store) error { return s.Put("", 1) }, ErrKey, false},
		{"negative-value", nil, func(s *Store) error { return s.Put("x", -1) }, ErrValue, false},
		{"overflow", nil, func(s *Store) error { return s.Put("x", 6) }, ErrValue, false}, // 2*6>10
		{"dualwrite-fault", migrateFault, func(s *Store) error { return s.Put("z", 1) }, ErrDualWrite, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := New(10)
			s.Put("a", 3)
			if c.setup != nil {
				c.setup(s)
			}
			p0, v10, v20 := s.s.Snapshot()
			if err := c.op(s); !errors.Is(err, c.want) {
				t.Fatalf("err=%v, want %v", err, c.want)
			}
			p1, v11, v21 := s.s.Snapshot()
			if p0 != p1 || !reflect.DeepEqual(v10, v11) || !reflect.DeepEqual(v20, v21) {
				t.Fatal("rejected op changed state")
			}
			if s.Get("a") != 3 || !c.fault && (s.Put("b", 2) != nil || s.Get("b") != 2) {
				t.Fatal("store unusable after rejection") // 被拒后仍可正常使用
			}
		})
	}
}
func migrateFault(s *Store) error {
	s.BeginMigration()
	s.InjectDualWriteFault()
	return nil
}

// SelfCheck 通过；缺失键 Get 返回 0；四类哨兵错误互不相同。
func TestSelfCheck(t *testing.T) {
	if err := New(1000).SelfCheck(); err != nil {
		t.Fatal(err)
	}
	if got := New(10).Get("nope"); got != 0 {
		t.Fatalf("Get(missing)=%d, want 0", got)
	}
	sents := []error{ErrPhase, ErrKey, ErrValue, ErrDualWrite}
	for i, a := range sents {
		for _, b := range sents[i+1:] {
			if errors.Is(a, b) || errors.Is(b, a) {
				t.Fatalf("%v and %v not distinct", a, b)
			}
		}
	}
}

// 并发：N 个 goroutine 并发 Get 同键结果一致；并发 Put 不同键后各键正确（不用 sleep）。
func TestConcurrentGetPut(t *testing.T) {
	for _, n := range []int{4, 16, 64} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			s := New(1 << 20)
			s.Put("hot", 7)
			var wg sync.WaitGroup
			start := make(chan struct{})
			for i := 0; i < n; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					<-start
					if s.Get("hot") != 7 {
						t.Error("get hot != 7")
					}
					if err := s.Put(fmt.Sprintf("k%d", i), i+1); err != nil {
						t.Errorf("put: %v", err)
					}
				}(i)
			}
			close(start)
			wg.Wait()
			for i := 0; i < n; i++ {
				if got := s.Get(fmt.Sprintf("k%d", i)); got != i+1 {
					t.Errorf("Get(k%d)=%d, want %d", i, got, i+1)
				}
			}
		})
	}
}

// 阶段推进、读写、SelfCheck 并发安全（交给 race 检测）。
func TestConcurrentMixed(t *testing.T) {
	s := New(1 << 20)
	ops := []func(){
		func() { s.Put("k", 1) }, func() { s.Get("k") }, func() { s.BeginMigration() },
		func() { s.Backfill() }, func() { s.Switch() }, func() { s.SelfCheck() },
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, op := range ops {
		wg.Add(1)
		go func(f func()) { defer wg.Done(); <-start; f() }(op)
	}
	close(start)
	wg.Wait()
}
