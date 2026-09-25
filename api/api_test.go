package api_test

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"ontology/api"
)

// 不变量 1：随机 Apply/Compact 交错序列后，Get 与朴素模型逐 Key 相同。
func TestNaiveModelConsistency(t *testing.T) {
	for _, n := range []int{10, 100, 1000} {
		rng := rand.New(rand.NewSource(int64(n)))
		s := api.New(n + 1)
		naive := map[string]int64{}
		keys := []string{"a", "b", "c", "d"}
		for i := 0; i < n; i++ {
			k := keys[rng.Intn(len(keys))]
			d := int64(rng.Intn(21) - 10) // 含负与零
			if err := s.Apply(k, d); err != nil {
				t.Fatalf("n=%d apply: %v", n, err)
			}
			naive[k] += d
			if rng.Intn(4) == 0 {
				if err := s.Compact(); err != nil {
					t.Fatalf("n=%d compact: %v", n, err)
				}
			}
		}
		for _, k := range keys {
			if got := s.Get(k); got != naive[k] {
				t.Fatalf("n=%d Get(%q)=%d, naive=%d", n, k, got, naive[k])
			}
		}
	}
}

// 不变量 2：任意一次 Compact 前后，所有 Key 的 Get 逐字段一致。
func TestCompactPreservesValues(t *testing.T) {
	cases := [][]int64{{1, 2, 3}, {-7, 0, 7}, {0, 0, 0}, {1000000, -999999, -1}}
	for _, ds := range cases {
		s := api.New(len(ds))
		before := map[string]int64{}
		for i, d := range ds {
			k := fmt.Sprintf("k%d", i)
			if err := s.Apply(k, d); err != nil {
				t.Fatal(err)
			}
			before[k] = s.Get(k)
		}
		if err := s.Compact(); err != nil {
			t.Fatal(err)
		}
		for k, w := range before {
			if got := s.Get(k); got != w {
				t.Fatalf("ds=%v: Compact changed Get(%q): %d -> %d", ds, k, w, got)
			}
		}
		if got := s.DeltaCount(); got != 0 {
			t.Fatalf("ds=%v: DeltaCount after Compact=%d, want 0", ds, got)
		}
	}
}

// 不变量 3：Apply(key,d) 使该 Key 的 Get 恰好增加 d，其他 Key 不变。
func TestApplyDeltaEffect(t *testing.T) {
	for _, d := range []int64{5, -5, 0, -1, 123456789} {
		s := api.New(8)
		_ = s.Apply("x", 10)
		_ = s.Apply("y", 20)
		bx, by := s.Get("x"), s.Get("y")
		if err := s.Apply("x", d); err != nil {
			t.Fatal(err)
		}
		if got := s.Get("x"); got != bx+d {
			t.Fatalf("d=%d: Get(x)=%d, want %d", d, got, bx+d)
		}
		if got := s.Get("y"); got != by {
			t.Fatalf("d=%d: Get(y)=%d, want unchanged %d", d, got, by)
		}
	}
}

// 不变量 4：三类拒绝互不相同、不留痕，拒绝后实例仍可正常使用。
func TestRejectionLeavesNoTrace(t *testing.T) {
	bad := api.New(0)
	errBad := bad.Apply("k", 1)
	if !errors.Is(errBad, api.ErrInvalidMaxDeltas) {
		t.Fatalf("New(0) apply err=%v, want ErrInvalidMaxDeltas", errBad)
	}
	s := api.New(2)
	_ = s.Apply("a", 3)
	cnt, va := s.DeltaCount(), s.Get("a")
	errKey := s.Apply("", 9)
	_ = s.Apply("a", 1) // 填满容量
	errCap := s.Apply("a", 1)
	if !errors.Is(errKey, api.ErrEmptyKey) || !errors.Is(errCap, api.ErrCapacityExceeded) {
		t.Fatalf("errKey=%v errCap=%v", errKey, errCap)
	}
	if errBad == errKey || errKey == errCap || errBad == errCap {
		t.Fatal("three rejection errors must be mutually distinct")
	}
	if s.DeltaCount() != cnt+1 || s.Get("a") != va+1 {
		t.Fatal("rejected Apply mutated state")
	}
	if s.Compact() != nil || s.Get("a") != 4 || s.Apply("b", 1) != nil || s.SelfCheck() != nil {
		t.Fatal("instance unusable after rejections")
	}
}

// 并发：N 个 goroutine 并发只读同一已填充实例，结果逐字段相同；不用 sleep。
func TestConcurrentReads(t *testing.T) {
	s := api.New(64)
	keys := []string{"a", "b", "c"}
	want := map[string]int64{}
	for i, k := range keys {
		for j := 0; j <= i; j++ {
			_ = s.Apply(k, int64(j+1))
		}
		want[k] = s.Get(k)
	}
	_ = s.Compact()
	_ = s.Apply("a", -1)
	want["a"]--
	start := make(chan struct{})
	var wg sync.WaitGroup
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for r := 0; r < 50; r++ {
				for _, k := range keys {
					if got := s.Get(k); got != want[k] {
						t.Errorf("Get(%q)=%d want %d", k, got, want[k])
					}
				}
				if s.DeltaCount() != 1 || s.SelfCheck() != nil {
					t.Errorf("DeltaCount=%d SelfCheck=%v", s.DeltaCount(), s.SelfCheck())
				}
			}
		}()
	}
	close(start)
	wg.Wait()
}
