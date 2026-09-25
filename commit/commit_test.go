package commit

import (
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
)

func run(n int, f func(int)) { // 并发执行 n 个 f(i)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Go(func() { f(i) })
	}
	wg.Wait()
}
func TestMerge(t *testing.T) {
	cases := []struct {
		batch []Op
		want  map[string]int64
	}{
		{[]Op{{"a", 1}, {"a", 2}, {"b", 3}}, map[string]int64{"a": 3, "b": 3}},
		{[]Op{{"a", 1}, {"a", -1}, {"b", 2}}, map[string]int64{"b": 2}},
	}
	for i, tc := range cases {
		net, _ := merge(tc.batch)
		for k, v := range tc.want {
			if net[k] != v {
				t.Errorf("case %d: net[%q]=%d, want %d", i, k, net[k], v)
			}
		}
	}
}
func TestCheckedKeysConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		e, _ := New(1)
		for i := 0; i < m; i++ {
			e.Commit([]Op{{strconv.Itoa(i), 1}})
		}
		e.Commit([]Op{{"only", 1}})
		if e.lastChecked != 1 {
			t.Errorf("m=%d: lastChecked=%d, want 1", m, e.lastChecked)
		}
	}
}
func TestVersionExactlyPlusOne(t *testing.T) {
	e, _ := New(4)
	e.Commit([]Op{{"a", 1}, {"b", 2}})
	e.Commit([]Op{{"a", 3}})
	for k, want := range map[string]uint64{"a": 2, "b": 1} {
		if e.cells[k].Ver != want {
			t.Errorf("ver[%s]=%d, want %d", k, e.cells[k].Ver, want)
		}
	}
}
func TestSixStep(t *testing.T) {
	e, _ := New(3)
	n1 := map[string]int64{"a": 1, "c": 1}
	n2 := map[string]int64{"b": 5, "c": 10}
	s1, s2 := e.snapshotOf(n1), e.snapshotOf(n2)
	ok1, conflict := e.tryCommit(s1), !e.tryCommit(s2)
	ok2 := e.tryCommit(e.snapshotOf(n2))
	v := e.View()
	if !ok1 || !conflict || !ok2 || v["a"] != 1 || v["b"] != 5 || v["c"] != 11 {
		t.Fatalf("六步交错错误: %v %v %v %v", ok1, conflict, ok2, v)
	}
}
func TestRejectionNoTrace(t *testing.T) {
	errs := []error{ErrEmptyBatch, ErrEmptyKey, ErrZeroDelta, ErrBadConfig, ErrContended}
	for i, a := range errs {
		for _, b := range errs[i+1:] {
			if errors.Is(a, b) {
				t.Fatalf("哨兵错误不互异: %v vs %v", a, b)
			}
		}
	}
	if _, err := New(0); !errors.Is(err, ErrBadConfig) {
		t.Fatalf("New(0) err=%v, want ErrBadConfig", err)
	}
	e, _ := New(2)
	e.Commit([]Op{{"a", 5}})
	for _, b := range [][]Op{nil, {{"", 1}}, {{"a", 0}}, {{"a", 1}, {"a", -1}}} {
		if e.Commit(b) == nil {
			t.Errorf("batch %v 应被拒", b)
		}
	}
	if e.View()["a"] != 5 || e.Retries() != 0 || e.cells["a"].Ver != 1 || e.Commit([]Op{{"a", 1}}) != nil {
		t.Error("被拒后状态改变或不可继续使用")
	}
}
func TestNaiveReplay(t *testing.T) {
	for _, n := range []int{2, 8, 32} {
		e, _ := New(n * 4)
		exp := map[string]int64{}
		for i := 0; i < n; i++ {
			exp["k"+strconv.Itoa(i%3)] += int64(i + 1)
		}
		run(n, func(i int) {
			if e.Commit([]Op{{"k" + strconv.Itoa(i%3), int64(i + 1)}}) != nil {
				t.Error("Commit 失败")
			}
		})
		for k, v := range exp {
			if e.View()[k] != v {
				t.Errorf("n=%d: view[%s]=%d, want %d", n, k, e.View()[k], v)
			}
		}
	}
}
func TestConcurrentSameKey(t *testing.T) {
	e, _ := New(64)
	var done atomic.Bool
	var rd sync.WaitGroup
	for i := 0; i < 4; i++ {
		rd.Go(func() {
			prev := int64(0)
			for !done.Load() {
				r := e.Retries()
				if r < prev {
					t.Error("Retries 回退")
				}
				prev = r
				_ = e.View()
			}
		})
	}
	run(64, func(int) { e.Commit([]Op{{"hot", 1}}) })
	done.Store(true)
	rd.Wait()
	if e.View()["hot"] != 64 {
		t.Errorf("hot=%d, want 64", e.View()["hot"])
	}
}
func TestContendedNoTrace(t *testing.T) {
	e, _ := New(1)
	var okCnt, conCnt atomic.Int64
	run(64, func(int) {
		switch e.Commit([]Op{{"hot", 1}}) {
		case nil:
			okCnt.Add(1)
		case ErrContended:
			conCnt.Add(1)
		}
	})
	if okCnt.Load()+conCnt.Load() != 64 || e.View()["hot"] != okCnt.Load() || e.Retries() != 0 {
		t.Errorf("ok=%d con=%d hot=%d retries=%d", okCnt.Load(), conCnt.Load(), e.View()["hot"], e.Retries())
	}
}
