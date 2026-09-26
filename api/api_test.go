package api_test

import (
	"errors"
	"math/rand"
	"sync"
	"testing"

	"ontology/api"
)

// TestNaiveAgreement pins invariant 1: loop-generated random streams match a naive O(n) scan (ties -> smallest index) on the chosen flow and V.
func TestNaiveAgreement(t *testing.T) {
	cases := [][]int{{2, 1}, {1, 2, 3, 5}, {7, 1, 4, 2, 9}}
	for ci, w := range cases {
		n, rng := len(w), rand.New(rand.NewSource(int64(ci)+1))
		q, _ := api.New(w)
		F := make([]int, n)
		qs := make([][]int, n)
		V, live := 0, 0
		for i := 0; i < 500 || live > 0; i++ {
			if live == 0 || (i < 500 && rng.Intn(2) == 0) {
				f, sz := rng.Intn(n), rng.Intn(9)+1
				if err := q.Submit(f, sz); err != nil {
					t.Fatal(err)
				}
				F[f] = max(F[f], V) + (sz+w[f]-1)/w[f]
				qs[f], live = append(qs[f], F[f]), live+1
				continue
			}
			got, err := q.Dequeue()
			pick := -1
			for f := 0; f < n; f++ { // ascending scan keeps smallest index on ties
				if len(qs[f]) > 0 && (pick < 0 || qs[f][0] < qs[pick][0]) {
					pick = f
				}
			}
			if err != nil || got != pick {
				t.Fatalf("got %d want %d (%v)", got, pick, err)
			}
			V, qs[pick], live = qs[pick][0], qs[pick][1:], live-1
		}
	}
	// The built-in self-check (naive agreement incl. V, monotonicity, fairness, rejection) must pass.
	if q, err := api.New([]int{2, 1}); err != nil || !q.SelfCheck() {
		t.Fatalf("SelfCheck=false err=%v", err)
	}
}

// TestFairnessOrder pins invariant 3 and the (丙) tie rule.
func TestFairnessOrder(t *testing.T) {
	cases := []struct {
		w    []int
		size int
		want []int
	}{
		{[]int{4, 2, 1}, 8, []int{0, 1, 2}}, // finishes 2,4,8
		{[]int{3, 1}, 3, []int{0, 1}},
		{[]int{2, 1}, 1, []int{0, 1}}, // tie 1==1 -> smallest index (丙)
	}
	for _, tc := range cases {
		q, _ := api.New(tc.w)
		for f := range tc.w {
			if err := q.Submit(f, tc.size); err != nil {
				t.Fatal(err)
			}
		}
		for _, want := range tc.want {
			if got, err := q.Dequeue(); err != nil || got != want {
				t.Fatalf("w%v: %d!=%d", tc.w, got, want)
			}
		}
	}
}

// TestRejectionLeavesNoTrace pins invariant 4 / section five.
func TestRejectionLeavesNoTrace(t *testing.T) {
	for _, w := range [][]int{nil, {}, {0}, {1, -1}} {
		if q, err := api.New(w); q != nil || !errors.Is(err, api.ErrConfig) {
			t.Fatalf("New(%v): want ErrConfig", w)
		}
	}
	if errors.Is(api.ErrSubmit, api.ErrEmpty) || errors.Is(api.ErrConfig, api.ErrSubmit) || errors.Is(api.ErrConfig, api.ErrEmpty) {
		t.Fatal("sentinels must be pairwise distinct")
	}
	q, _ := api.New([]int{2, 1})
	for _, c := range [][2]int{{-1, 1}, {9, 1}, {0, 0}, {1, -1}} {
		if err := q.Submit(c[0], c[1]); !errors.Is(err, api.ErrSubmit) {
			t.Fatal("want ErrSubmit")
		}
	}
	if _, err := q.Dequeue(); !errors.Is(err, api.ErrEmpty) || q.VirtualTime() != 0 {
		t.Fatal("rejected ops left a trace")
	}
	_ = q.Submit(0, 2) // subsequent successful dequeues prove these took effect
	_ = q.Submit(1, 1)
	if f, err := q.Dequeue(); err != nil || f != 0 {
		t.Fatalf("want flow 0, got %d", f)
	}
	if f, err := q.Dequeue(); err != nil || f != 1 || q.VirtualTime() != 1 {
		t.Fatalf("want flow 1,V=1, got %d,V=%d", f, q.VirtualTime())
	}
}

// TestConcurrentSubmit pins section six: N goroutines, one packet each; then exactly N ascending dequeue, concurrent V never backwards. No sleep.
func TestConcurrentSubmit(t *testing.T) {
	for _, N := range []int{1, 16, 128, 500} {
		q, _ := api.New([]int{1})
		done := make(chan struct{})
		var rwg, swg sync.WaitGroup
		rwg.Add(1)
		go func() {
			defer rwg.Done()
			last := 0
			for {
				select {
				case <-done:
					return
				default:
					v := q.VirtualTime()
					if v < last {
						t.Errorf("V backwards %d -> %d", last, v)
						return
					}
					last = v
				}
			}
		}()
		for i := 1; i <= N; i++ {
			swg.Add(1)
			go func(sz int) { defer swg.Done(); _ = q.Submit(0, sz) }(i)
		}
		swg.Wait()
		last := 0
		for i := 0; i < N; i++ {
			f, err := q.Dequeue()
			if err != nil || f != 0 || q.VirtualTime() <= last {
				t.Fatalf("N=%d i=%d f=%d %v", N, i, f, err)
			}
			last = q.VirtualTime()
		}
		if _, err := q.Dequeue(); !errors.Is(err, api.ErrEmpty) {
			t.Fatalf("N=%d: want exactly N then ErrEmpty", N)
		}
		close(done)
		rwg.Wait()
	}
}
