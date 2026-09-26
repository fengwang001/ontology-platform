package api

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

func lcg(r *int64, m int) int { // 确定性伪随机数，供表驱动用例生成操作序列
	*r = *r*6364136223846793005 + 1
	return int(uint64(*r>>33) % uint64(m))
}

// TestNaiveConsistency 不变量 1：任意操作序列下与朴素 O(n) 扫描参照一致。
func TestNaiveConsistency(t *testing.T) {
	cases := [][]int{{1}, {2, 1}, {3, 1, 2, 5}, {1, 1, 1, 1, 1}, {7, 3, 9}}
	for i, weights := range cases {
		rng := int64(i + 1)
		w, _ := New(weights)
		ref := &naive{w: weights, fin: make([]int, len(weights)), q: make([][]int, len(weights))}
		for op := 0; op < 300; op++ {
			if lcg(&rng, 3) > 0 {
				f, sz := lcg(&rng, len(weights)), lcg(&rng, 11)+1
				w.Submit(f, sz) // 入参合法必成功；若失败后续比对会暴露
				ref.submit(f, sz)
			} else {
				got, err := w.Dequeue()
				want, ok := ref.dequeue()
				if (err == nil) != ok || (err == nil && got != want) {
					t.Fatalf("case %v op %d: got (%d,%v) want (%d,%v)", weights, op, got, err, want, ok)
				}
			}
		}
	}
}

// TestMonotonic 不变量 2：每次 Submit 后该流 F 单调不减，流内包完成时刻升序。
func TestMonotonic(t *testing.T) {
	for _, weights := range [][]int{{1}, {2, 1}, {3, 1, 2, 5}} {
		w, _ := New(weights)
		rng := int64(len(weights))
		prevF := make([]int, len(weights))
		for op := 0; op < 300; op++ {
			if lcg(&rng, 3) > 0 {
				f := lcg(&rng, len(weights))
				w.Submit(f, lcg(&rng, 9)+1)
				if w.s.Finish(f) < prevF[f] || !w.s.HeadsAscending() {
					t.Fatalf("weights %v op %d: F not monotonic", weights, op)
				}
				prevF[f] = w.s.Finish(f)
			} else {
				w.Dequeue()
			}
		}
	}
}

// TestFairness 不变量 3：权重大（inc 小）的流完成时刻小、先出队且服务份额大。
func TestFairness(t *testing.T) {
	cases := []struct {
		weights []int
		subs    [][2]int // (flow, size) 提交序列
		want    []int    // 期望出队流序列
	}{
		{[]int{1, 4}, [][2]int{{0, 4}, {1, 4}}, []int{1, 0}},
		{[]int{2, 1}, [][2]int{{0, 2}, {1, 2}}, []int{0, 1}},
		{[]int{1, 1}, [][2]int{{0, 3}, {1, 3}}, []int{0, 1}},                                       // 并列取下标最小
		{[]int{1, 3}, [][2]int{{0, 3}, {1, 3}, {0, 3}, {1, 3}, {0, 3}, {1, 3}}, []int{1, 1, 0, 1}}, // 份额 3:1
	}
	for _, c := range cases {
		w, _ := New(c.weights)
		for _, s := range c.subs {
			w.Submit(s[0], s[1])
		}
		for i, want := range c.want {
			if got, _ := w.Dequeue(); got != want {
				t.Fatalf("weights %v dequeue %d: got %d, want %d", c.weights, i, got, want)
			}
		}
	}
}

// TestFailureAtomic 不变量 4：被拒操作可判定、互不相同、不改变状态、之后可用。
func TestFailureAtomic(t *testing.T) {
	if _, err := New(nil); !errors.Is(err, ErrEmptyWeights) {
		t.Fatal("empty weights")
	}
	if _, err := New([]int{1, 0}); !errors.Is(err, ErrBadWeight) {
		t.Fatal("bad weight")
	}
	w, _ := New([]int{2})
	w.Submit(0, 2)
	badF, badS, badW := []int{1, -1, 0, 0}, []int{1, 1, 0, -3}, []error{ErrFlowRange, ErrFlowRange, ErrBadSize, ErrBadSize}
	v0, f0, l0 := w.VirtualTime(), w.s.Finish(0), w.s.QueueLen(0)
	for i := range badF {
		if err := w.Submit(badF[i], badS[i]); !errors.Is(err, badW[i]) {
			t.Fatalf("submit(%d,%d): got %v, want %v", badF[i], badS[i], err, badW[i])
		}
	}
	if w.VirtualTime() != v0 || w.s.Finish(0) != f0 || w.s.QueueLen(0) != l0 {
		t.Fatal("rejected op changed state")
	}
	if _, err := w.Dequeue(); err != nil {
		t.Fatal("unusable after rejection")
	}
	if _, err := w.Dequeue(); !errors.Is(err, ErrEmpty) {
		t.Fatal("empty dequeue not rejected")
	}
	if ErrEmptyWeights == ErrBadWeight || ErrFlowRange == ErrBadSize || ErrEmpty == ErrBadSize {
		t.Fatal("sentinels not distinct")
	}
}

func TestConcurrentSubmit(t *testing.T) {
	for _, n := range []int{16, 64, 256} {
		w, _ := New([]int{2})
		var wg sync.WaitGroup
		var hi atomic.Int64 // 期间任一时刻读到的 V 不得小于已见最大值
		var bad atomic.Bool
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				w.Submit(0, 2)
				v := int64(w.VirtualTime())
				if v < hi.Load() {
					bad.Store(true)
				}
				hi.CompareAndSwap(hi.Load(), v)
			}()
		}
		wg.Wait()
		for i := 0; i < n; i++ { // 全部 size=2、w=2：完成时刻恰为 1..N
			if _, err := w.Dequeue(); err != nil || w.VirtualTime() != i+1 {
				t.Fatalf("n=%d: packet %d out of order", n, i)
			}
		}
		if _, err := w.Dequeue(); !errors.Is(err, ErrEmpty) || bad.Load() {
			t.Fatalf("n=%d: queue length wrong or V decreased", n)
		}
	}
}

func TestSelfCheck(t *testing.T) { // 内置自检五项全部通过
	w, _ := New([]int{1})
	if err := errors.Join(w.SelfCheck()...); err != nil {
		t.Fatal(err)
	}
}
