package api

import (
	"errors"
	"math/rand"
	"sync"
	"testing"
)

func mustNew(t *testing.T, max int) *DeltaStore {
	d, err := New(max)
	if err != nil {
		t.Fatalf("New(%d): %v", max, err)
	}
	return d
}

// TestNaiveReference 不变量1：随机 Apply/Compact 交错序列后，逐 Key 与朴素参照一致。
func TestNaiveReference(t *testing.T) {
	keys := []string{"a", "b", "c"}
	for seed := int64(0); seed < 50; seed++ {
		r := rand.New(rand.NewSource(seed))
		d := mustNew(t, 100000)
		naive := map[string]int64{}
		for i := 0; i < 300; i++ {
			if r.Intn(5) == 0 {
				_ = d.Compact()
				continue
			}
			k := keys[r.Intn(len(keys))]
			v := int64(r.Intn(21) - 10) // 含负数与 0
			_ = d.Apply(k, v)
			naive[k] += v
		}
		for _, k := range keys {
			if got := d.Get(k); got != naive[k] {
				t.Fatalf("seed=%d key=%s: Get=%d， 朴素=%d", seed, k, got, naive[k])
			}
		}
	}
}

// TestCompactPreservesValues 不变量2：Compact 前后所有 Key 的 Get 逐字段一致。
func TestCompactPreservesValues(t *testing.T) {
	d := mustNew(t, 100)
	keys := []string{"a", "b", "c"}
	for i, k := range keys {
		for j := 0; j <= i; j++ {
			_ = d.Apply(k, int64(j-1))
		}
	}
	before := map[string]int64{}
	for _, k := range keys {
		before[k] = d.Get(k)
	}
	_ = d.Compact()
	for _, k := range keys {
		if got := d.Get(k); got != before[k] {
			t.Fatalf("key=%s: Compact 前 %d 后 %d", k, before[k], got)
		}
	}
	if n := d.DeltaCount(); n != 0 {
		t.Fatalf("Compact 后 DeltaCount=%d", n)
	}
}

// TestApplyDeltaEffect 不变量3：Apply(key,d) 使该 Key 增加 d，其他 Key 不变。
func TestApplyDeltaEffect(t *testing.T) {
	for _, delta := range []int64{10, -4, 0, -100} {
		d := mustNew(t, 100)
		_ = d.Apply("a", 3)
		_ = d.Apply("b", 7)
		a0, b0 := d.Get("a"), d.Get("b")
		_ = d.Apply("a", delta)
		if got := d.Get("a"); got != a0+delta {
			t.Fatalf("d=%d: Get(a)=%d， want %d", delta, got, a0+delta)
		}
		if got := d.Get("b"); got != b0 {
			t.Fatalf("d=%d: 其他 Key 受影响 Get(b)=%d", delta, got)
		}
	}
}

// TestRejectedOpsLeaveNoTrace 不变量4：三类错误可判定、互不相同、被拒后状态不变。
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	for _, max := range []int{0, -3} {
		if _, err := New(max); !errors.Is(err, ErrNonPositiveMaxDeltas) {
			t.Fatalf("New(%d) err=%v", max, err)
		}
	}
	d := mustNew(t, 2)
	_ = d.Apply("a", 5)
	before := d.Get("a")
	if err := d.Apply("", 1); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("空 Key err=%v", err)
	}
	_ = d.Apply("b", 1) // 占满容量
	if err := d.Apply("c", 1); !errors.Is(err, ErrTooManyDeltas) {
		t.Fatalf("超容量 err=%v", err)
	}
	if ErrEmptyKey == ErrTooManyDeltas || ErrEmptyKey == ErrNonPositiveMaxDeltas ||
		ErrTooManyDeltas == ErrNonPositiveMaxDeltas {
		t.Fatal("三类哨兵错误必须互不相同")
	}
	if d.Get("a") != before || d.Get("c") != 0 || d.DeltaCount() != 2 {
		t.Fatal("被拒操作改变了状态")
	}
	if err := d.Compact(); err != nil || d.Get("a") != before {
		t.Fatal("被拒后实例不可继续使用")
	}
}

// TestConcurrentReaders 并发只读：N 个 goroutine 拿到的所有 Key 值逐字段相同。
func TestConcurrentReaders(t *testing.T) {
	d := mustNew(t, 1000)
	keys := []string{"a", "b", "c"}
	for i, k := range keys {
		_ = d.Apply(k, int64(10*(i+1)))
	}
	want := map[string]int64{}
	for _, k := range keys {
		want[k] = d.Get(k)
	}
	var wg sync.WaitGroup
	errs := make(chan string, 64)
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				for _, k := range keys {
					if d.Get(k) != want[k] {
						errs <- k
					}
				}
				if d.DeltaCount() != 3 {
					errs <- "DeltaCount"
				}
				if err := d.SelfCheck(); err != nil {
					errs <- err.Error()
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatalf("并发读不一致: %s", e)
	}
}
