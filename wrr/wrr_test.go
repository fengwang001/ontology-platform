package wrr

import (
	"sync"
	"testing"
)

func TestNextSequence312(t *testing.T) {
	b := New([]int{3, 1, 2})
	want := []int{0, 2, 0, 1, 2, 0}
	for k, w := range want {
		if got := b.Next(); got != w {
			t.Fatalf("step %d: got %d want %d", k+1, got, w)
		}
	}
}

// TestFairnessAndSumCW: each W-block picks server i exactly w_i times; sum(cw)==0 after every Next.
func TestFairnessAndSumCW(t *testing.T) {
	cases := [][]int{{3, 1, 2}, {1}, {1, 1, 1}, {2, 5, 3, 7, 1}, {9, 1, 1, 1}}
	seed := uint32(42)
	rnd := func(n int) int { seed = seed*1664525 + 1013904223; return int(seed>>8)%n + 1 }
	for m := 2; m <= 50; m++ {
		w := make([]int, m)
		for i := range w {
			w[i] = rnd(20)
		}
		cases = append(cases, w)
	}
	for _, w := range cases {
		b, total := New(w), 0
		for _, x := range w {
			total += x
		}
		for rep := 0; rep < 2; rep++ { // two consecutive W-blocks
			got := make([]int, len(w))
			for k := 0; k < total; k++ {
				got[b.Next()]++
				if s := b.SumCW(); s != 0 {
					t.Fatalf("w=%v: sum(cw) = %d after a Next", w, s)
				}
			}
			for i := range w {
				if got[i] != w[i] {
					t.Fatalf("w=%v block %d: server %d picked %d, want %d", w, rep, i, got[i], w[i])
				}
			}
		}
	}
}

// TestDeterminism: identical configs reproduce identical sequences.
func TestDeterminism(t *testing.T) {
	a, b := New([]int{5, 3, 8, 1}), New([]int{5, 3, 8, 1})
	for k := 0; k < 200; k++ {
		if x, y := a.Next(), b.Next(); x != y {
			t.Fatalf("step %d: %d != %d", k, x, y)
		}
	}
}

// TestSetWeightReset: SetWeight recomputes W, resets every cw to 0, and fairness holds for the new weights.
func TestSetWeightReset(t *testing.T) {
	b := New([]int{3, 1, 2})
	b.Next()
	b.Next()
	b.SetWeight(1, 5)
	if s := b.SumCW(); s != 0 {
		t.Fatalf("cw not reset: sum = %d", s)
	}
	if b.Weight(1) != 5 || b.Len() != 3 {
		t.Fatalf("weight not updated: w[1]=%d len=%d", b.Weight(1), b.Len())
	}
	got := make([]int, 3)
	for k := 0; k < 10; k++ { // new W = 3+5+2
		got[b.Next()]++
	}
	for i, want := range []int{3, 5, 2} {
		if got[i] != want {
			t.Fatalf("server %d picked %d, want %d", i, got[i], want)
		}
	}
}

// TestMaxLocationChecksSublinear: one Next on a fresh m-server balancer (distinct weights)
// costs a constant number of comparisons independent of m — heap root, no full scan.
func TestMaxLocationChecksSublinear(t *testing.T) {
	const bound = 64
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		w := make([]int, m)
		for i := range w {
			w[i] = i + 1 // distinct weights
		}
		b := New(w)
		b.Next()
		if b.lastChecks > bound {
			t.Fatalf("m=%d: %d comparisons > bound %d", m, b.lastChecks, bound)
		}
	}
	if err := CheckScalability(); err != nil {
		t.Fatal(err)
	}
}

// TestConcurrentSumCW: concurrent Next keeps fairness exact; a concurrent reader never sees sum(cw)!=0. No sleeps.
func TestConcurrentSumCW(t *testing.T) {
	b := New([]int{3, 1, 2})
	const G, per = 8, 300 // 2400 picks = 400 full cycles of W=6
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				if s := b.SumCW(); s != 0 {
					t.Errorf("observed sum(cw) = %d", s)
					return
				}
			}
		}
	}()
	counts := make([]int, 3)
	var mu sync.Mutex
	for g := 0; g < G; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			local := make([]int, 3)
			for k := 0; k < per; k++ {
				local[b.Next()]++
			}
			mu.Lock()
			for i := range counts {
				counts[i] += local[i]
			}
			mu.Unlock()
		}()
	}
	close(stop)
	wg.Wait()
	for i, want := range []int{1200, 400, 800} {
		if counts[i] != want {
			t.Fatalf("server %d picked %d times, want %d", i, counts[i], want)
		}
	}
}
