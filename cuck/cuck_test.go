package cuck

import (
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/hash"
)

// 确定性伪随机操作序列，返回跑完序列的过滤器与存活键集合。
func runRandom(t *testing.T, numBuckets, epb, kicks, ops, domain int) (*Filter, map[int64]bool) {
	f, err := New(numBuckets, epb, kicks)
	if err != nil {
		t.Fatal(err)
	}
	live := map[int64]bool{}
	var seed uint64 = 42
	rand := func(n int64) int64 {
		seed = seed*6364136223846793005 + 1442695040888963407
		return int64(seed>>16) % n
	}
	for i := 0; i < ops; i++ {
		x := rand(int64(domain))
		if rand(3) == 0 && live[x] {
			if err := f.Delete(x); err != nil {
				t.Fatalf("delete %d: %v", x, err)
			}
			delete(live, x)
		} else if !live[x] {
			if err := f.Insert(x); err == nil {
				live[x] = true
			} else if !errors.Is(err, ErrFull) {
				t.Fatalf("insert %d: %v", x, err)
			}
		}
	}
	return f, live
}

func TestNoFalseNegatives(t *testing.T) {
	for _, c := range []struct{ nb, epb, kicks, ops, domain int }{
		{4, 2, 4, 200, 50}, {16, 4, 8, 1000, 200}, {256, 4, 16, 5000, 1000},
	} {
		f, live := runRandom(t, c.nb, c.epb, c.kicks, c.ops, c.domain)
		for x := int64(0); x < int64(c.domain); x++ {
			if live[x] && !f.Lookup(x) {
				t.Fatalf("false negative: %d", x)
			}
		}
	}
}

func TestLookupMatchesNaive(t *testing.T) {
	f, _ := runRandom(t, 64, 4, 8, 2000, 300)
	for x := int64(0); x < 300; x++ {
		fp := hash.Fingerprint(x)
		i1, i2 := f.cand(x)
		want := find(f.buckets[i1], fp) >= 0 || find(f.buckets[i2], fp) >= 0
		if got := f.Lookup(x); got != want {
			t.Fatalf("Lookup(%d)=%v, naive=%v", x, got, want)
		}
	}
}

func TestFingerprintConservation(t *testing.T) {
	for _, c := range []struct{ nb, epb, kicks, ops, domain int }{
		{4, 2, 4, 300, 60}, {32, 4, 8, 1500, 150},
	} {
		f, live := runRandom(t, c.nb, c.epb, c.kicks, c.ops, c.domain)
		n := 0
		for _, b := range f.Buckets() {
			n += len(b)
		}
		if n != len(live) || n != len(f.inserted) {
			t.Fatalf("fingerprints %d, live %d, inserted %d", n, len(live), len(f.inserted))
		}
	}
}

func TestCheckedBucketsConstant(t *testing.T) {
	for _, nb := range []int{128, 256, 512, 1024, 2048, 4096, 8192} { // 100..10000 内的 2 的幂
		f, _ := New(nb, 4, 8)
		for x := int64(0); x < 100; x++ {
			_ = f.Insert(x)
		}
		f.Lookup(7)
		got1 := f.checked.Load()
		_ = f.Delete(7)
		if got1 != 2 || f.checked.Load() != 2 {
			t.Fatalf("numBuckets=%d: checked %d then %d, want 2", nb, got1, f.checked.Load())
		}
	}
}

func TestRollbackOnFull(t *testing.T) {
	f, _ := New(2, 1, 1)
	if f.Insert(0) != nil || f.Insert(2) != nil {
		t.Fatal("setup insert failed")
	}
	before := f.Buckets()
	if err := f.Insert(4); !errors.Is(err, ErrFull) {
		t.Fatalf("want ErrFull, got %v", err)
	}
	if !reflect.DeepEqual(f.Buckets(), before) || len(f.inserted) != 2 {
		t.Fatal("state changed after ErrFull")
	}
}

func TestRejectedOpsKeepState(t *testing.T) {
	f, _ := New(4, 2, 4)
	_ = f.Insert(1)
	before := f.Buckets()
	if f.Insert(-5) == nil || f.Delete(999) == nil || f.Delete(-1) == nil {
		t.Fatal("rejected op returned nil")
	}
	if !reflect.DeepEqual(f.Buckets(), before) || !f.Lookup(1) {
		t.Fatal("rejected ops changed state")
	}
}

func TestConcurrentLookup(t *testing.T) {
	f, _ := New(256, 4, 16)
	for x := int64(0); x < 600; x++ {
		_ = f.Insert(x)
	}
	want := map[int64]bool{}
	for x := int64(0); x < 1200; x++ {
		want[x] = f.Lookup(x)
	}
	var wg sync.WaitGroup
	var bad atomic.Bool
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for x := int64(0); x < 1200; x++ {
				if f.Lookup(x) != want[x] {
					bad.Store(true)
				}
			}
		}()
	}
	wg.Wait()
	if bad.Load() {
		t.Fatal("concurrent Lookup mismatch")
	}
}
