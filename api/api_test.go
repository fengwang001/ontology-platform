package api_test

import (
	"errors"
	"math/rand"
	"sync"
	"testing"

	"ontology/api"
	"ontology/vv"
)

// naive 朴素重算：枚举并集所有 key、逐 key 取 max（语义比较，缺失视为 0）。
func naive(a, b vv.Vector) vv.Vector {
	out := vv.Vector{}
	for k, va := range a {
		if va > b[k] {
			out[k] = va
		} else {
			out[k] = b[k]
		}
	}
	for k, vb := range b {
		if _, ok := a[k]; !ok {
			out[k] = vb
		}
	}
	return out
}

func sameVec(a, b vv.Vector) bool {
	for k, va := range a {
		if va != b[k] {
			return false
		}
	}
	for k, vb := range b {
		if _, ok := a[k]; !ok && vb != 0 {
			return false
		}
	}
	return true
}

func randVec(rng *rand.Rand, space int) vv.Vector {
	v := vv.Vector{}
	for i := 0; i < space; i++ {
		if rng.Intn(2) == 0 {
			v[rng.Intn(space)] = rng.Intn(5)
		}
	}
	return v
}

// TestMergeAgainstNaive 不变量 1：随机向量、随机规模，Merge 结果对拍朴素重算。
func TestMergeAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	for i := 0; i < 100; i++ {
		a, b := randVec(rng, 2+i%20), randVec(rng, 2+i%20)
		got, want := vv.Merge(a, b), naive(a, b)
		if !sameVec(got, want) {
			t.Fatalf("iter %d: Merge(%v,%v)=%v, naive=%v", i, a, b, got, want)
		}
	}
}

// TestErrorsDistinct 三类哨兵错误两两不同且均可被 errors.Is 判定。
func TestErrorsDistinct(t *testing.T) {
	a := api.New()
	e1 := a.Set("x", vv.Vector{0: -1})
	e2 := a.Set("x", vv.Vector{-1: 0})
	_, e3 := a.Merge("x", "y")
	_, e4 := a.Compare("y", "x")
	for _, e := range []error{e1, e2, e3, e4} {
		if e == nil {
			t.Fatal("expected error, got nil")
		}
	}
	if !errors.Is(e1, api.ErrNegativeCounter) || !errors.Is(e2, api.ErrNegativeActor) ||
		!errors.Is(e3, api.ErrUnknownName) || !errors.Is(e4, api.ErrUnknownName) {
		t.Fatal("errors.Is mismatch")
	}
	if e1 == e2 || e2 == e3 || e1 == e3 {
		t.Fatal("sentinel errors are not distinct")
	}
}

// TestFailureLeavesNoTrace 不变量 4：被拒操作不改状态，注册表仍可正常使用。
func TestFailureLeavesNoTrace(t *testing.T) {
	a := api.New()
	if a.Set("r1", vv.Vector{0: 1}) != nil || a.Set("r2", vv.Vector{0: 1, 1: 2}) != nil {
		t.Fatal("setup set failed")
	}
	before, _ := a.Merge("r1", "r2")
	cb, _ := a.Compare("r1", "r2")
	// 一串被拒操作：负计数器、负 actor、未注册名字。
	_ = a.Set("r1", vv.Vector{0: -9})
	_ = a.Set("r2", vv.Vector{-3: 1})
	_ = a.Set("ghost", vv.Vector{0: 1, 2: -1})
	_, _ = a.Merge("r1", "ghost")
	_, _ = a.Compare("ghost", "r2")
	after, err := a.Merge("r1", "r2")
	if err != nil || !sameVec(after, before) {
		t.Fatalf("registry mutated by rejected ops: %v vs %v", after, before)
	}
	if ca, _ := a.Compare("r1", "r2"); ca != cb {
		t.Fatalf("compare changed: %v vs %v", ca, cb)
	}
	if err := a.Set("r3", vv.Vector{2: 1}); err != nil { // 拒绝后仍可用
		t.Fatalf("registry unusable after rejects: %v", err)
	}
}

// TestConcurrentMergeConsistent N 个 goroutine 并发 Merge 同一对，结果逐 key 相同。
func TestConcurrentMergeConsistent(t *testing.T) {
	a := api.New()
	_ = a.Set("r2", vv.Vector{0: 1, 1: 2})
	_ = a.Set("r3", vv.Vector{1: 1, 2: 1})
	want, _ := a.Merge("r2", "r3")
	const n = 64
	results := make([]vv.Vector, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, err := a.Merge("r2", "r3")
			if err != nil {
				t.Errorf("concurrent merge: %v", err)
			}
			results[i] = r
		}(i)
	}
	wg.Wait()
	for i, r := range results {
		if !sameVec(r, want) {
			t.Fatalf("goroutine %d got %v, want %v", i, r, want)
		}
	}
}

// TestSelfCheck 内置自检必须通过。
func TestSelfCheck(t *testing.T) {
	if err := api.New().SelfCheck(); err != nil {
		t.Fatalf("selfcheck: %v", err)
	}
}
