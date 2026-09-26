package api

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/gc"
	"ontology/reg"
)

func TestSelfCheck(t *testing.T) {
	if err := New().SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

// TestSixStep 钉住 NOTES.md 的六步推导表。
func TestSixStep(t *testing.T) {
	a := New()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(a.Set("a", nil))
	must(a.Inc("a", 0, 5))
	must(a.Set("b", nil))
	must(a.Inc("b", 1, 3))
	if v, _ := a.MergeInto("b", "a"); v != 8 {
		t.Errorf("S3 Value(b)=%d, 期望 8", v)
	}
	must(a.Inc("a", 0, 2))
	if v, _ := a.MergeInto("b", "a"); v != 10 {
		t.Errorf("S5 Value(b)=%d, 期望 10", v)
	}
	must(a.Set("c", nil))
	must(a.Inc("c", 0, 1))
	must(a.Set("d", nil))
	must(a.Inc("d", 1, 1))
	if v, _ := a.MergeInto("c", "d"); v != 2 {
		t.Errorf("S6 后 Value(c)=%d, 期望 2", v)
	}
}

// TestMonotonic 不变量3：表驱动操作序列中 Value 单调不减。
func TestMonotonic(t *testing.T) {
	cases := [][][2]int{ // 每个元素是 {node, k} 序列
		{{0, 1}, {1, 2}, {0, 3}},
		{{5, 7}, {5, 1}, {9, 4}},
	}
	for _, ops := range cases {
		a := New()
		if err := a.Set("m", nil); err != nil {
			t.Fatal(err)
		}
		prev := 0
		for _, op := range ops {
			if err := a.Inc("m", op[0], op[1]); err != nil {
				t.Fatal(err)
			}
			v, _ := a.Value("m")
			if v < prev {
				t.Errorf("Value 减小: %d < %d", v, prev)
			}
			prev = v
		}
	}
}

// TestFailureNoTrace 不变量4：三类错误可判定、互不相同、被拒后状态不变且可继续用。
func TestFailureNoTrace(t *testing.T) {
	if reg.ErrNonPositiveIncrement == reg.ErrNegativeNode ||
		reg.ErrNegativeNode == gc.ErrNegativeEntry ||
		reg.ErrNonPositiveIncrement == gc.ErrNegativeEntry {
		t.Fatal("三类错误不互异")
	}
	cases := []struct {
		name string
		run  func(a *API) error
		want error
	}{
		{"非正增量", func(a *API) error { return a.Inc("x", 0, 0) }, reg.ErrNonPositiveIncrement},
		{"负增量", func(a *API) error { return a.Inc("x", 0, -3) }, reg.ErrNonPositiveIncrement},
		{"负node", func(a *API) error { return a.Inc("x", -1, 1) }, reg.ErrNegativeNode},
		{"负条目注册", func(a *API) error { return a.Set("y", map[int]int{0: -1}) }, gc.ErrNegativeEntry},
	}
	for _, tc := range cases {
		a := New()
		if err := a.Set("x", map[int]int{0: 5}); err != nil {
			t.Fatal(err)
		}
		if err := tc.run(a); !errors.Is(err, tc.want) {
			t.Errorf("%s: err=%v, 期望 %v", tc.name, err, tc.want)
		}
		if v, _ := a.Value("x"); v != 5 {
			t.Errorf("%s: 被拒后状态改变 Value=%d", tc.name, v)
		}
		if err := a.Inc("x", 0, 2); err != nil {
			t.Errorf("%s: 被拒后不可继续用: %v", tc.name, err)
		}
	}
}

// TestConcurrentInc M 个 goroutine 并发对不同 node 做 Inc，
// 结束后 Value 等于各增量之和，期间并发读到的 Value 单调不减。
func TestConcurrentInc(t *testing.T) {
	const M = 64
	a := New()
	if err := a.Set("c", nil); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var done, bad atomic.Bool
	for i := 0; i < 4; i++ { // 并发读者
		wg.Add(1)
		go func() {
			defer wg.Done()
			prev := 0
			for !done.Load() {
				v, err := a.Value("c")
				if err != nil || v < prev {
					bad.Store(true)
					return
				}
				prev = v
			}
		}()
	}
	var writers sync.WaitGroup
	for i := 0; i < M; i++ {
		writers.Add(1)
		go func(node int) {
			defer writers.Done()
			_ = a.Inc("c", node, node+1)
		}(i)
	}
	writers.Wait()
	done.Store(true)
	wg.Wait()
	if bad.Load() {
		t.Error("并发读到的 Value 非单调")
	}
	want := M * (M + 1) / 2 // 各增量为 1..M
	if v, _ := a.Value("c"); v != want {
		t.Errorf("并发后 Value=%d, 期望 %d", v, want)
	}
}
