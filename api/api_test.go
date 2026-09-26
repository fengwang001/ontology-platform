package api_test

import (
	"errors"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/api"
)

// TestLookupAfterInsertDelete 钉住不变量 1：Insert 后 Lookup 必真，Delete 后必假。
func TestLookupAfterInsertDelete(t *testing.T) {
	for _, n := range []int{1, 7, 64, 1024} {
		h, _ := api.New(n, 4*n) // n >= 1，不会报错
		var keys []int
		for i := 0; i < n/2; i++ {
			x := i*3 + 1
			if err := h.Insert(x); err != nil {
				t.Fatalf("n=%d Insert(%d): %v", n, x, err)
			}
			keys = append(keys, x)
		}
		for _, x := range keys {
			if ok, err := h.Lookup(x); !ok || err != nil {
				t.Fatalf("n=%d Lookup(%d) after insert = %v,%v", n, x, ok, err)
			}
			if err := h.Delete(x); err != nil {
				t.Fatalf("n=%d Delete(%d): %v", n, x, err)
			}
			if ok, _ := h.Lookup(x); ok {
				t.Fatalf("n=%d Lookup(%d) true after delete", n, x)
			}
		}
	}
}

// TestMatchesNaiveMap 钉住不变量 2：随机 Insert/Delete 序列后与朴素参照逐键一致。
func TestMatchesNaiveMap(t *testing.T) {
	for _, seed := range []int64{1, 2, 3, 42, 99} {
		rng := rand.New(rand.NewSource(seed))
		h, _ := api.New(4096, 512)
		ref := map[int]bool{}
		for op := 0; op < 2000; op++ {
			x := rng.Intn(3000) - 500 // 含负数键
			if rng.Intn(2) == 0 {
				if err := h.Insert(x); err == nil {
					ref[x] = true
				} else if !errors.Is(err, api.ErrExists) && !errors.Is(err, api.ErrTableFull) {
					t.Fatalf("seed=%d Insert(%d): %v", seed, x, err)
				}
			} else if err := h.Delete(x); err == nil {
				delete(ref, x)
			} else if !errors.Is(err, api.ErrNotFound) {
				t.Fatalf("seed=%d Delete(%d): %v", seed, x, err)
			}
		}
		for x := -600; x < 3100; x++ {
			if ok, _ := h.Lookup(x); ok != ref[x] {
				t.Fatalf("seed=%d Lookup(%d) mismatch", seed, x)
			}
		}
		if h.Len() != len(ref) {
			t.Fatalf("seed=%d Len=%d, ref=%d", seed, h.Len(), len(ref))
		}
	}
}

// TestRejectedOpsNoSideEffect 钉住不变量 4：四类错误互异，被拒后状态不变且可继续用。
func TestRejectedOpsNoSideEffect(t *testing.T) {
	sent := []error{api.ErrExists, api.ErrNotFound, api.ErrTableFull, api.ErrInvalidParam}
	for i := range sent {
		for j := range sent {
			if (i == j) != errors.Is(sent[i], sent[j]) {
				t.Fatalf("sentinel %d and %d not distinct", i, j)
			}
		}
	}
	for _, n := range []int{0, -5} {
		if _, err := api.New(n, 1); !errors.Is(err, api.ErrInvalidParam) {
			t.Fatalf("New(%d,1) = %v, want ErrInvalidParam", n, err)
		}
	}
	h, _ := api.New(4, 8)
	for x := 0; x < 8; x++ {
		_ = h.Insert(x)
	}
	n0 := h.Len()
	b1, b2 := h.Snapshot()
	lookupMiss := func() error { _, e := h.Lookup(99); return e }
	rejects := []error{h.Insert(3), h.Insert(8), lookupMiss(), h.Delete(99)}
	want := []error{api.ErrExists, api.ErrTableFull, api.ErrNotFound, api.ErrNotFound}
	for i := range rejects {
		if !errors.Is(rejects[i], want[i]) {
			t.Fatalf("reject %d = %v, want %v", i, rejects[i], want[i])
		}
	}
	a1, a2 := h.Snapshot()
	if h.Len() != n0 {
		t.Fatalf("Len changed after rejects: %d -> %d", n0, h.Len())
	}
	for i := range b1 {
		if a1[i] != b1[i] || a2[i] != b2[i] {
			t.Fatalf("slot %d changed after rejected ops", i)
		}
	}
	if err := h.Delete(3); err != nil { // 被拒后仍可正常使用
		t.Fatalf("unusable after rejects: %v", err)
	}
}

// TestConcurrentReadOnly 钉住并发约束：N 个 goroutine 并发只读，结果与参照一致，不用 sleep。
func TestConcurrentReadOnly(t *testing.T) {
	h, _ := api.New(4096, 512)
	ref := map[int]bool{}
	for x := 0; x < 2000; x += 2 {
		if err := h.Insert(x); err != nil {
			t.Fatal(err)
		}
		ref[x] = true
	}
	const g = 16
	var wg sync.WaitGroup
	var failed atomic.Bool
	start := make(chan struct{})
	for w := 0; w < g; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for x := -100; x < 2200; x++ {
				ok, err := h.Lookup(x)
				if ok != ref[x] || (ok && err != nil) || (!ok && !errors.Is(err, api.ErrNotFound)) {
					failed.Store(true)
					return
				}
			}
			if _ = h.Len(); h.SelfCheck() != nil { // Len/SelfCheck 并发安全
				failed.Store(true)
			}
		}()
	}
	close(start) // 同时放行，不用 sleep
	wg.Wait()
	if failed.Load() {
		t.Fatal("concurrent read-only mismatch")
	}
}
