package api

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
)

func fillN(t *testing.T, a *API, n int) {
	t.Helper()
	es := make([]string, n)
	for i := range es {
		es[i] = fmt.Sprintf("e%d", i)
	}
	if err := a.Feed(es); err != nil {
		t.Fatalf("Feed: %v", err)
	}
}

func TestFacadeSizeSample(t *testing.T) {
	cases := []struct{ k, n, wantLen int }{
		{3, 0, 0}, {3, 2, 2}, {3, 3, 3}, {3, 50, 3}, {10, 4, 4},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("k=%d,n=%d", c.k, c.n), func(t *testing.T) {
			a, err := New(c.k, func(i int) int { return (i % 3) + 1 })
			if err != nil {
				t.Fatal(err)
			}
			fillN(t, a, c.n)
			if a.Size() != c.n {
				t.Fatalf("Size=%d want %d", a.Size(), c.n)
			}
			if got := a.Sample(); len(got) != c.wantLen {
				t.Fatalf("Sample len=%d want %d", len(got), c.wantLen)
			}
		})
	}
}

func TestFourErrorsDistinct(t *testing.T) {
	cases := []struct {
		name string
		call func() error
		want error
	}{
		{"bad capacity", func() error { _, e := New(0, func(int) int { return 1 }); return e }, ErrBadCapacity},
		{"nil rng", func() error { _, e := New(3, nil); return e }, ErrNilRNG},
		{"empty element", func() error {
			a, _ := New(3, func(int) int { return 1 })
			return a.Feed([]string{"a", ""})
		}, ErrEmptyElement},
		{"j out of range", func() error {
			a, _ := New(3, func(i int) int { return i + 1 }) // i+1 > i
			return a.Feed([]string{"a", "b", "c", "d"})
		}, ErrJOutOfRange},
	}
	seen := map[error]bool{}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := c.call(); !errors.Is(err, c.want) {
				t.Fatalf("err=%v want %v", err, c.want)
			}
			seen[c.want] = true
		})
	}
	if len(seen) != 4 {
		t.Fatalf("four sentinels must be distinct, got %d unique", len(seen))
	}
}

func TestRejectedBatchAtomic(t *testing.T) {
	a, _ := New(3, func(int) int { return 1 })
	fillN(t, a, 6)
	saved := a.Sample()
	badBatches := [][]string{{"g", ""}, {"", "g"}, {"g"}}
	// 最后一批配越界 rng 单独实例验证 j 越界。
	if err := a.Feed(badBatches[0]); !errors.Is(err, ErrEmptyElement) {
		t.Fatalf("err=%v", err)
	}
	if err := a.Feed(badBatches[1]); !errors.Is(err, ErrEmptyElement) {
		t.Fatalf("err=%v", err)
	}
	if !reflect.DeepEqual(a.Sample(), saved) || a.Size() != 6 {
		t.Fatalf("state changed: %v size=%d", a.Sample(), a.Size())
	}
	b, _ := New(3, func(i int) int { return i + 1 })
	fillN(t, b, 3)
	if err := b.Feed(badBatches[2]); !errors.Is(err, ErrJOutOfRange) {
		t.Fatalf("err=%v", err)
	}
	if b.Size() != 3 {
		t.Fatalf("j-out-of-range batch changed Size to %d", b.Size())
	}
	if err := a.Feed([]string{"g"}); err != nil { // 拒绝后仍可正常使用
		t.Fatalf("unusable after reject: %v", err)
	}
}

func TestSelfCheckPasses(t *testing.T) {
	a, err := New(3, func(int) int { return 1 })
	if err != nil {
		t.Fatal(err)
	}
	if err := a.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
	if a.Size() != 0 { // 自检不得改变接收者状态
		t.Fatalf("SelfCheck mutated receiver, Size=%d", a.Size())
	}
}

// 并发只读：N 个 goroutine 同时读一个已喂满的实例，结果必须逐槽相同。
// 用关闭 channel 同步起跑，不使用任何 sleep。
func TestConcurrentReadersIdentical(t *testing.T) {
	for _, n := range []int{2, 8, 64} {
		t.Run(fmt.Sprintf("goroutines=%d", n), func(t *testing.T) {
			a, _ := New(5, func(i int) int { return (2*i+1)%i + 1 })
			fillN(t, a, 1000)
			ref := a.Sample()

			results := make([][]string, n)
			sizes := make([]int, n)
			start := make(chan struct{})
			var wg sync.WaitGroup
			for g := 0; g < n; g++ {
				wg.Add(1)
				go func(g int) {
					defer wg.Done()
					<-start // 所有 goroutine 同时进入读区间
					results[g] = a.Sample()
					sizes[g] = a.Size()
				}(g)
			}
			close(start)
			wg.Wait()

			for g := 0; g < n; g++ {
				if !reflect.DeepEqual(results[g], ref) {
					t.Fatalf("goroutine %d sample %v != ref %v", g, results[g], ref)
				}
				if sizes[g] != 1000 {
					t.Fatalf("goroutine %d Size=%d want 1000", g, sizes[g])
				}
			}
		})
	}
}
