package api

import (
	"errors"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

// TestSentinelErrors pins the five mutually distinct, decidable failures and
// exercises SelfCheck on the built-in suite.
func TestSentinelErrors(t *testing.T) {
	cases := []struct {
		name string
		call func() error
		want error
	}{
		{"invalid n", func() error { _, e := New(0); return e }, ErrInvalidN},
		{"negative n", func() error { _, e := New(-3); return e }, ErrInvalidN},
		{"out of range u", func() error { h, _ := New(2); return h.AddEdge(2, 0) }, ErrOutOfRange},
		{"out of range v", func() error { h, _ := New(2); return h.AddEdge(0, -1) }, ErrOutOfRange},
		{"self loop", func() error { h, _ := New(2); return h.AddEdge(1, 1) }, ErrSelfLoop},
		{"duplicate", func() error { h, _ := New(2); _ = h.AddEdge(0, 1); return h.AddEdge(0, 1) }, ErrDuplicate},
	}
	seen := map[error]bool{}
	for _, c := range cases {
		if err := c.call(); !errors.Is(err, c.want) {
			t.Errorf("%s: err=%v, want %v", c.name, err, c.want)
		}
		seen[c.want] = true
	}
	if len(seen) != 4 { // invalid/out-of-range/self/duplicate must be pairwise distinct
		t.Fatalf("rejection sentinels not distinct: %v", seen)
	}
	cy, _ := New(3)
	for _, e := range [][2]int{{0, 1}, {1, 2}, {2, 0}} {
		_ = cy.AddEdge(e[0], e[1])
	}
	if _, _, err := cy.Solve(); !errors.Is(err, ErrCyclic) {
		t.Fatalf("cyclic Solve err=%v, want ErrCyclic", err)
	}
	h, _ := New(1)
	if err := h.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

func TestRejectLeavesStateUnchanged(t *testing.T) {
	cases := []struct {
		u, v int
		want error
	}{
		{5, 0, ErrOutOfRange}, {0, 5, ErrOutOfRange}, {-1, 0, ErrOutOfRange},
		{0, 0, ErrSelfLoop}, {0, 1, ErrDuplicate},
	}
	for _, c := range cases {
		h, _ := New(4)
		if err := h.AddEdge(0, 1); err != nil {
			t.Fatal(err)
		}
		before := h.EdgeCount()
		if err := h.AddEdge(c.u, c.v); !errors.Is(err, c.want) {
			t.Errorf("AddEdge(%d,%d) err=%v, want %v", c.u, c.v, err, c.want)
		}
		if h.EdgeCount() != before {
			t.Errorf("AddEdge(%d,%d) changed EdgeCount %d->%d", c.u, c.v, before, h.EdgeCount())
		}
		if err := h.AddEdge(1, 2); err != nil { // still usable after rejection
			t.Fatalf("graph unusable after rejection: %v", err)
		}
		if cover, _, err := h.Solve(); err != nil || cover != 2 {
			t.Fatalf("after rejection cover=%d err=%v, want 2", cover, err)
		}
	}
}

// TestRandomCovers pins invariant 1 over random DAGs/orders: edge-backed
// chains, vertex-disjoint, union exactly [0,n).
func TestRandomCovers(t *testing.T) {
	cases := []struct{ n, trials int }{{1, 1}, {2, 3}, {5, 10}, {8, 20}, {12, 20}}
	for _, c := range cases {
		for k := 0; k < c.trials; k++ {
			rng := rand.New(rand.NewSource(int64(c.n*7919 + k)))
			h, _ := New(c.n)
			exists := map[[2]int]bool{}
			for u := 0; u < c.n; u++ { // only forward edges => DAG
				for v := u + 1; v < c.n; v++ {
					if rng.Intn(2) == 0 {
						_ = h.AddEdge(u, v)
						exists[[2]int{u, v}] = true
					}
				}
			}
			cover, paths, err := h.Solve()
			if err != nil || cover != len(paths) {
				t.Fatalf("n=%d cover=%d paths=%d err=%v", c.n, cover, len(paths), err)
			}
			used, total := make([]bool, c.n), 0
			for _, p := range paths {
				for i, v := range p {
					if v < 0 || v >= c.n || used[v] || (i > 0 && !exists[[2]int{p[i-1], v}]) {
						t.Fatalf("n=%d illegal chain/duplicate at node %d: %v", c.n, v, paths)
					}
					used[v], total = true, total+1
				}
			}
			if total != c.n { // in-range + no duplicate + total n => each node exactly once
				t.Fatalf("n=%d cover has %d nodes, paths=%v", c.n, total, paths)
			}
		}
	}
}

// TestConcurrentSolve: on a frozen DAG, N goroutines get itemwise identical
// covers and partitions. Channel barrier, no sleep.
func TestConcurrentSolve(t *testing.T) {
	cases := []struct{ n, workers int }{{6, 4}, {20, 16}, {50, 32}}
	for _, c := range cases {
		h, _ := New(c.n)
		rng := rand.New(rand.NewSource(int64(c.n)))
		for u := 0; u < c.n; u++ {
			for v := u + 1; v < c.n; v++ {
				if rng.Intn(3) == 0 {
					_ = h.AddEdge(u, v)
				}
			}
		}
		covers := make([]int, c.workers)
		all := make([][][]int, c.workers)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for w := 0; w < c.workers; w++ {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				<-start
				covers[w], all[w], _ = h.Solve()
			}(w)
		}
		close(start)
		wg.Wait()
		for w := 1; w < c.workers; w++ {
			if covers[w] != covers[0] || !reflect.DeepEqual(all[w], all[0]) {
				t.Fatalf("n=%d worker %d: %d/%v, want %d/%v", c.n, w, covers[w], all[w], covers[0], all[0])
			}
		}
	}
}
