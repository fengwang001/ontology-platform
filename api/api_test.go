package api_test

import (
	"errors"
	"slices"
	"sync"
	"testing"

	"ontology/api"
	"ontology/sched"
)

// TestEightStep 第三节推导的八步序列，钉住每步放行/拒绝、出发时间与系统内项数。
func TestEightStep(t *testing.T) {
	l, _ := api.New(3, 5)
	steps := [][4]int64{ // {t, dep, admitted, inSystem}
		{0, 5, 1, 1}, {5, 10, 1, 1}, {6, 15, 1, 2}, {10, 20, 1, 2},
		{15, 25, 1, 2}, {15, 30, 1, 3}, {15, 0, 0, 3}, {21, 35, 1, 3},
	}
	for i, w := range steps {
		dep, ok, err := l.Submit(w[0])
		if err != nil || ok != (w[2] == 1) || (ok && dep != w[1]) || l.InSystem() != int(w[3]) {
			t.Errorf("step %d: got (%d,%v,%d,%v) want %v", i, dep, ok, l.InSystem(), err, w)
		}
	}
	if err := l.SelfCheck(); err != nil {
		t.Errorf("SelfCheck: %v", err)
	}
}

// naive 朴素参照：显式维护出发时间列表。
type naive struct {
	cap, interval int64
	deps          []int64
}

func (n *naive) submit(t int64) (int64, bool) {
	i := 0
	for i < len(n.deps) && n.deps[i] <= t {
		i++
	}
	n.deps = n.deps[i:]
	if int64(len(n.deps)) == n.cap {
		return 0, false
	}
	dep := t + n.interval
	if len(n.deps) > 0 {
		dep = n.deps[len(n.deps)-1] + n.interval
	}
	n.deps = append(n.deps, dep)
	return dep, true
}

// TestNaiveConsistency 不变量 1/2/3：随机序列下与朴素参照一致、平滑、容量不越界。
func TestNaiveConsistency(t *testing.T) {
	cfgs := []struct{ cap, interval, seed int64 }{{1, 1, 7}, {2, 3, 8}, {3, 5, 9}, {5, 2, 10}, {10, 100, 11}}
	for _, c := range cfgs {
		l, _ := api.New(c.cap, c.interval)
		n := &naive{cap: c.cap, interval: c.interval}
		seed, tt, prev := uint64(c.seed), int64(0), int64(0)
		for i := 0; i < 500; i++ {
			seed = seed*6364136223846793005 + 1442695040888963407
			tt += int64(seed >> 33 % (3 * uint64(c.interval)))
			dep, ok, err := l.Submit(tt)
			ndep, nok := n.submit(tt)
			bad := err != nil || ok != nok || (ok && dep != ndep) || l.InSystem() != len(n.deps)
			if ok {
				bad = bad || (prev != 0 && dep-prev < c.interval)
				prev = dep
			}
			if bad || int64(l.InSystem()) > c.cap {
				t.Fatalf("cfg %+v i=%d t=%d: got (%d,%v,%d) want (%d,%v,%d)",
					c, i, tt, dep, ok, l.InSystem(), ndep, nok, len(n.deps))
			}
		}
	}
}

// TestFailureAtomicity 不变量 4：三类错误可判定且互不相同，被拒后不留痕、可继续用。
func TestFailureAtomicity(t *testing.T) {
	for _, c := range [][2]int64{{0, 1}, {-3, 1}, {1, 0}, {1, -5}} {
		if _, err := api.New(c[0], c[1]); !errors.Is(err, api.ErrConfig) {
			t.Errorf("New(%d,%d): err=%v", c[0], c[1], err)
		}
	}
	if api.ErrConfig == sched.ErrNegativeT || api.ErrConfig == sched.ErrNonMonotonic || sched.ErrNegativeT == sched.ErrNonMonotonic {
		t.Fatal("哨兵错误不互异")
	}
	l, _ := api.New(2, 5)
	twin, _ := api.New(2, 5)
	for _, tt := range []int64{0, 3, 3, 10} {
		l.Submit(tt)
		twin.Submit(tt)
	}
	before := l.InSystem()
	for i, tt := range []int64{-1, 9} {
		if _, _, err := l.Submit(tt); !errors.Is(err, []error{sched.ErrNegativeT, sched.ErrNonMonotonic}[i]) {
			t.Errorf("t=%d: err=%v", tt, err)
		}
	}
	if l.InSystem() != before {
		t.Fatal("被拒后系统内项数改变")
	}
	for _, tt := range []int64{12, 12, 20, 21} { // 与孪生实例行为一致即未留痕
		d1, o1, _ := l.Submit(tt)
		d2, o2, _ := twin.Submit(tt)
		if d1 != d2 || o1 != o2 {
			t.Fatalf("t=%d: 拒绝留痕", tt)
		}
	}
}

// TestConcurrentSubmit 并发：同一时刻提交，接纳数不超容量，出发时间互异且递增。
func TestConcurrentSubmit(t *testing.T) {
	const n, cap = 64, 10
	l, _ := api.New(cap, 5)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var deps []int64
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			dep, ok, err := l.Submit(1000)
			if err != nil {
				t.Errorf("err %v", err)
			}
			mu.Lock()
			if ok {
				deps = append(deps, dep)
			}
			mu.Unlock()
			_ = l.InSystem()
			_ = l.SelfCheck()
		}()
	}
	wg.Wait()
	if len(deps) > cap {
		t.Errorf("接纳 %d 超容量 %d", len(deps), cap)
	}
	slices.Sort(deps)
	for i := 1; i < len(deps); i++ {
		if deps[i]-deps[i-1] < 5 {
			t.Errorf("出发时间不互异或不递增: %v", deps)
		}
	}
}
