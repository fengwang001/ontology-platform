package api_test

import (
	"math/rand"
	"slices"
	"sync"
	"testing"

	"ontology/api"
)

func mustNew(t *testing.T, m, k int) *api.Filter {
	t.Helper()
	f, err := api.New(m, k)
	if err != nil {
		t.Fatalf("New(%d,%d): %v", m, k, err)
	}
	return f
}

// TestNoFalseNegative 不变量 1：Add 后（未 Remove）Query ≥ 1。
func TestNoFalseNegative(t *testing.T) {
	for _, tc := range []struct{ m, k, n int }{{64, 3, 50}, {256, 4, 200}, {1024, 5, 500}} {
		f := mustNew(t, tc.m, tc.k)
		for x := int64(0); x < int64(tc.n); x++ {
			if err := f.Add(x); err != nil {
				t.Fatal(err)
			}
		}
		for x := int64(0); x < int64(tc.n); x++ {
			if v, _ := f.Query(x); v < 1 {
				t.Fatalf("m=%d: false negative on key %d", tc.m, x)
			}
		}
	}
}

// TestExactRemoval 不变量 2：单元素 Add 再 Remove 后所有计数器归 0。
func TestExactRemoval(t *testing.T) {
	for _, x := range []int64{0, 1, 7, 100, 99999} {
		f := mustNew(t, 64, 4)
		f.Add(x)
		if err := f.Remove(x); err != nil {
			t.Fatalf("Remove(%d): %v", x, err)
		}
		for i, v := range f.Snapshot() {
			if v != 0 {
				t.Fatalf("key %d: counter %d = %d, want 0", x, i, v)
			}
		}
	}
}

// TestNaiveReplayConsistency 不变量 3：任意操作序列后计数器等于朴素重放。
func TestNaiveReplayConsistency(t *testing.T) {
	const m, k = 64, 4
	for seed := int64(1); seed <= 5; seed++ {
		f := mustNew(t, m, k)
		model := make([]int64, m)
		rng := rand.New(rand.NewSource(seed))
		for i := 0; i < 500; i++ {
			key := rng.Int63n(40)
			if rng.Intn(2) == 0 {
				f.Add(key)
				for j := int64(1); j <= k; j++ {
					model[(j*key)%m]++
				}
			} else if err := f.Remove(key); err == nil {
				for j := int64(1); j <= k; j++ {
					model[(j*key)%m]--
				}
			}
		}
		if !slices.Equal(f.Snapshot(), model) {
			t.Fatalf("seed %d: snapshot != naive replay", seed)
		}
	}
}

// TestFailedOpNoSideEffect 不变量 4：所有被拒操作不改变任何计数器，之后仍可用。
func TestFailedOpNoSideEffect(t *testing.T) {
	for _, tc := range []struct{ m, k int }{{0, 3}, {-1, 3}, {3, 0}, {3, -2}} {
		if _, err := api.New(tc.m, tc.k); err != api.ErrInvalidParam {
			t.Fatalf("New(%d,%d) err = %v", tc.m, tc.k, err)
		}
	}
	f := mustNew(t, 8, 3)
	f.Add(3)
	f.Add(5)
	before := f.Snapshot()
	ops := []struct {
		run  func() error
		want error
	}{
		{func() error { return f.Add(-1) }, api.ErrInvalidKey},
		{func() error { return f.Remove(-1) }, api.ErrInvalidKey},
		{func() error { _, e := f.Query(-1); return e }, api.ErrInvalidKey},
		{func() error { return f.Remove(4) }, api.ErrNotPresent}, // 4 从未加入
	}
	for _, op := range ops {
		if err := op.run(); err != op.want {
			t.Fatalf("err = %v, want %v", err, op.want)
		}
	}
	if !slices.Equal(f.Snapshot(), before) {
		t.Fatal("rejected ops mutated counters")
	}
	if err := f.Add(9); err != nil {
		t.Fatalf("Add after rejections: %v", err)
	}
}

// TestErrorsDistinct 三类哨兵错误互不相同。
func TestErrorsDistinct(t *testing.T) {
	e := []error{api.ErrInvalidParam, api.ErrInvalidKey, api.ErrNotPresent}
	if e[0] == e[1] || e[1] == e[2] || e[0] == e[2] {
		t.Fatalf("sentinel errors not distinct: %v", e)
	}
}

// TestConcurrentQueryConsistent 并发 Query/SelfCheck：逐 key 结果相同，无 sleep。
func TestConcurrentQueryConsistent(t *testing.T) {
	f := mustNew(t, 256, 4)
	const n = 128
	for x := int64(0); x < n; x++ {
		f.Add(x)
	}
	const goroutines = 8
	res := make([][]int64, goroutines)
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			res[id] = make([]int64, n)
			for x := int64(0); x < n; x++ {
				res[id][x], _ = f.Query(x)
			}
			if err := f.SelfCheck(); err != nil {
				t.Errorf("concurrent SelfCheck: %v", err)
			}
		}(g)
	}
	wg.Wait()
	for g := 1; g < goroutines; g++ {
		if !slices.Equal(res[0], res[g]) {
			t.Fatalf("goroutine %d results differ", g)
		}
	}
}
