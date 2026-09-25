package api

import (
	"errors"
	"sync"
	"testing"
)

func fatalIf(t *testing.T, bad bool, msg string, args ...any) {
	t.Helper()
	if bad {
		t.Fatalf(msg, args...)
	}
}

// 不变量 1：任意交错序列下与朴素参照逐次一致（表驱动多档规模）。
func TestMatchesNaive(t *testing.T) {
	for _, m := range []int{1, 2, 3, 17, 256} {
		got, err := New(m)
		fatalIf(t, err != nil, "New(%d): %v", m, err)
		ref := &naiveQ{maxLen: m}
		seed := uint32(m*2246822519 + 7)
		for i := 0; i < 2000; i++ {
			seed = seed*1664525 + 1013904223
			if seed>>30 <= 1 { // Enqueue 概率 1/2
				ge, re := got.Enqueue(i), ref.Enqueue(i)
				fatalIf(t, errors.Is(ge, ErrFull) != errors.Is(re, ErrFull), "m=%d i=%d enq %v vs %v", m, i, ge, re)
			} else {
				gv, gok := got.Dequeue()
				rv, rok := ref.Dequeue()
				fatalIf(t, gv != rv || gok != rok, "m=%d i=%d deq (%d,%v) vs (%d,%v)", m, i, gv, gok, rv, rok)
			}
			fatalIf(t, got.Len() != ref.Len(), "m=%d i=%d len %d vs %d", m, i, got.Len(), ref.Len())
		}
	}
}

// 不变量 2：FIFO 且守恒，Len 恒在 [0,maxLen]。
func TestFIFOAndConservation(t *testing.T) {
	for _, m := range []int{1, 5, 1000} {
		qu, _ := New(m)
		for round := 0; round < 3; round++ { // 循环多轮充放
			for i := 0; i < m; i++ {
				fatalIf(t, qu.Enqueue(i) != nil, "m=%d enq %d", m, i)
				l := qu.Len()
				fatalIf(t, l != i+1 || l < 0 || l > m, "m=%d len=%d", m, l)
			}
			for i := 0; i < m; i++ {
				v, ok := qu.Dequeue()
				fatalIf(t, !ok || v != i, "m=%d deq %d = (%d,%v)", m, i, v, ok)
			}
			fatalIf(t, qu.Len() != 0, "m=%d drained len=%d", m, qu.Len())
		}
	}
}

// 不变量 3：空/满精确判定。
func TestEmptyFullExact(t *testing.T) {
	qu, _ := New(2)
	v, ok := qu.Dequeue()
	fatalIf(t, ok || v != 0, "empty deq = (%d,%v)", v, ok)
	_ = qu.Enqueue(10)
	_ = qu.Enqueue(20)
	fatalIf(t, !errors.Is(qu.Enqueue(30), ErrFull), "full not ErrFull")
	fatalIf(t, qu.Len() != 2, "len=%d after full reject", qu.Len())
}

// 不变量 4：被拒操作零副作用，之后队列仍正常；Close 为终态且排空不丢。
func TestRejectionLeavesNoTrace(t *testing.T) {
	for _, bad := range []int{0, -3} {
		_, err := New(bad)
		fatalIf(t, !errors.Is(err, ErrBadMaxLen), "New(%d)=%v", bad, err)
	}
	qu, _ := New(2)
	_ = qu.Enqueue(7)
	_ = qu.Enqueue(8)
	_ = qu.Enqueue(9) // 满，被拒
	fatalIf(t, qu.Len() != 2, "len=%d after full reject", qu.Len())
	fatalIf(t, qu.Close() != nil, "first close")
	fatalIf(t, !errors.Is(qu.Enqueue(1), ErrClosed), "enq after close")
	fatalIf(t, !errors.Is(qu.Close(), ErrClosed), "double close")
	for _, want := range []int{7, 8} { // 关闭后排空，内容不变
		v, ok := qu.Dequeue()
		fatalIf(t, !ok || v != want, "drain=(%d,%v) want %d", v, ok, want)
	}
	_, ok := qu.Dequeue()
	fatalIf(t, ok, "drained not empty")
}

// 三个哨兵错误互不相同。
func TestErrorsDistinct(t *testing.T) {
	errs := []error{ErrBadMaxLen, ErrFull, ErrClosed}
	for i := range errs {
		for j := range errs {
			fatalIf(t, i != j && errors.Is(errs[i], errs[j]), "errs[%d]==errs[%d]", i, j)
		}
	}
}

// 并发：N producer 各入唯一值、N consumer 并发出队，集合恰好一致。
func TestConcurrentMPMC(t *testing.T) {
	for _, n := range []int{64, 1024, 8192} {
		qu, _ := New(n)
		var wg sync.WaitGroup
		got := make(chan int, n)
		for i := 0; i < n; i++ {
			wg.Add(2)
			go func(v int) { defer wg.Done(); _ = qu.Enqueue(v) }(i)
			go func() {
				defer wg.Done()
				for {
					if v, ok := qu.Dequeue(); ok {
						got <- v
						return
					}
				}
			}()
		}
		wg.Wait()
		close(got)
		seen := make(map[int]int, n)
		for v := range got {
			seen[v]++
		}
		fatalIf(t, len(seen) != n || qu.Len() != 0, "n=%d distinct=%d len=%d", n, len(seen), qu.Len())
		for v, c := range seen {
			fatalIf(t, c != 1 || v < 0 || v >= n, "n=%d val=%d cnt=%d", n, v, c)
		}
	}
}

// SelfCheck 自身必须通过，且可并发调用。
func TestSelfCheck(t *testing.T) {
	qu, _ := New(4)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fatalIf(t, qu.SelfCheck() != nil, "SelfCheck: %v", qu.SelfCheck())
		}()
	}
	wg.Wait()
}
