package rwlock

import (
	"math"
	"testing"
)

func probeBound(n int) int { return 4*int(math.Ceil(math.Log2(float64(n+1)))) + 4 }

// TestEvaluationProbeBound drives creates and deletion-triggered
// re-evaluations at n=100 and n=10000 and asserts the non-exported AVL
// probe counter never exceeds 4*ceil(log2(n+1))+4.
func TestEvaluationProbeBound(t *testing.T) {
	for _, n := range []int{100, 10000} {
		co := New(int64(n) + 5)
		holder := co.Open()
		sess := make([]int64, n)
		for i := range sess {
			sess[i] = co.Open()
		}
		l := co.lock("L")
		// Held writer; alternate kinds to exercise both evaluation rules.
		co.Create(holder, "L", Write)
		for i := 1; i < n; i++ {
			kind := Read
			if i%2 == 0 {
				kind = Write
			}
			if _, err := co.Create(sess[i], "L", kind); err != nil {
				t.Fatalf("n=%d create %d: %v", n, i, err)
			}
			bound := probeBound(l.tree.size())
			if l.maxEvalProbes > bound {
				t.Fatalf("n=%d create probes %d exceed bound %d (size %d)",
					n, l.maxEvalProbes, bound, l.maxEvalN)
			}
		}
		// Release the head: each re-evaluation round must respect the bound.
		if _, err := co.Release(holder, "L", Write, 0); err != nil {
			t.Fatal(err)
		}
		for i, p := range l.lastProbes {
			if p > probeBound(l.tree.size()) {
				t.Fatalf("n=%d deletion reeval #%d probes %d > bound %d",
					n, i, p, probeBound(l.tree.size()))
			}
		}
		if l.maxEvalProbes > probeBound(n) {
			t.Fatalf("n=%d max eval probes %d > %d", n, l.maxEvalProbes, probeBound(n))
		}
	}
}

// TestNotificationsExactlyWatchers proves deleting a node watched by exactly
// k live waiters triggers exactly k re-evaluations and k is independent of
// total lock size (pure writer chain: k=1 for any n).
func TestNotificationsExactlyWatchers(t *testing.T) {
	for _, k := range []int{1, 5, 50} {
		co := New(100000)
		h := co.Open()
		co.Create(h, "L", Write) // W0 held, watched by all k readers below
		sess := make([]int64, k)
		for i := range sess {
			sess[i] = co.Open()
			r, err := co.Create(sess[i], "L", Read)
			mustOK(t, err)
			if r.Watching != 0 {
				t.Fatalf("reader %d should watch W0", i)
			}
		}
		l := co.lock("L")
		ev, err := co.Release(h, "L", Write, 0)
		mustOK(t, err)
		if l.lastReevals != k {
			t.Fatalf("k=%d reevals=%d want %d", k, l.lastReevals, k)
		}
		if len(ev) != k {
			t.Fatalf("k=%d grants=%d want %d", k, len(ev), k)
		}
		for _, p := range l.lastProbes {
			if p > probeBound(k+1) {
				t.Fatalf("k=%d probes %d > bound", k, p)
			}
		}
	}
}
