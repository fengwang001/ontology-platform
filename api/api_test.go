package api

import (
	"errors"
	"math/rand"
	"slices"
	"sync"
	"testing"
)

// TestInvProductIsIdentity pins invariant 1: table-driven sizes, random order, A·A⁻¹==I.
func TestInvProductIsIdentity(t *testing.T) {
	x := New()
	fixed := [][]float64{
		{0, 2, 1, 1}, // NOTES.md derivation matrix
		{-2, 0, 0, 3},
		{4, -1, 0, -1, 4, -1, 0, -1, 4},
	}
	for _, a := range fixed {
		n := 1
		for n*n < len(a) {
			n++
		}
		inv, err := x.Inv(a, n)
		if err != nil || deviation(a, inv, n) > eps {
			t.Fatalf("fixed n=%d dev=%g err=%v", n, deviation(a, inv, n), err)
		}
	}
	rng := rand.New(rand.NewSource(4242))
	for _, idx := range rng.Perm(7) {
		n := []int{1, 2, 3, 5, 8, 16, 33}[idx]
		a := make([]float64, n*n)
		for i := range a {
			a[i] = rng.Float64()*20 - 10
		}
		for i := 0; i < n; i++ { // strict diagonal dominance -> invertible
			a[i*n+i] += float64(n) * 10
		}
		inv, err := x.Inv(a, n)
		if err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		if d := deviation(a, inv, n); d > eps {
			t.Fatalf("n=%d A*A^-1 deviates %g", n, d)
		}
	}
}

// TestInputNotMutated pins invariant 3: the caller's slice is byte-identical after Inv.
func TestInputNotMutated(t *testing.T) {
	x := New()
	for _, a := range [][]float64{{-3}, {0, 2, 1, 1}, {4, -1, 0, -1, 4, -1, 0, -1, 4}} {
		n := 1
		for n*n < len(a) {
			n++
		}
		snap := slices.Clone(a)
		if _, err := x.Inv(a, n); err != nil || !slices.Equal(a, snap) {
			t.Fatalf("input changed or err=%v", err)
		}
	}
}

// TestRejectedCallsDoNotChangeState pins invariant 4: three distinct
// sentinels, no state advanced, and the service stays usable afterwards.
func TestRejectedCallsDoNotChangeState(t *testing.T) {
	x := New()
	if _, err := x.Inv([]float64{2, 0, 0, 2}, 2); err != nil {
		t.Fatal(err)
	}
	before := x.accepted.Load()
	bad := []struct {
		a    []float64
		n    int
		want error
	}{
		{nil, 0, ErrEmptyMatrix},
		{[]float64{1, 0, 0, 1}, 3, ErrBadDimension},
		{[]float64{1, 1, 2, 2}, 2, ErrSingular},
	}
	for _, b := range bad {
		if _, err := x.Inv(b.a, b.n); !errors.Is(err, b.want) {
			t.Fatalf("got %v want %v", err, b.want)
		}
	}
	if x.accepted.Load() != before {
		t.Fatal("rejected calls changed state")
	}
	good, err := x.Inv([]float64{1, 0, 0, 1}, 2)
	if err != nil || !slices.Equal(good, []float64{1, 0, 0, 1}) || x.accepted.Load() != before+1 {
		t.Fatalf("service not usable/counted after rejection: %v %v", good, err)
	}
	if ErrEmptyMatrix == ErrBadDimension || ErrBadDimension == ErrSingular ||
		ErrEmptyMatrix == ErrSingular {
		t.Fatal("sentinel errors are not distinct")
	}
}

// TestConcurrentInvariance pins section 6: N goroutines share one read-only
// input (no sleeps); results are byte-identical, input untouched, race clean.
func TestConcurrentInvariance(t *testing.T) {
	x := New()
	a := []float64{0, 2, 1, 1}
	snap := slices.Clone(a)
	const N = 64
	outs := make([][]float64, N)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			inv, err := x.Inv(a, 2)
			if err != nil {
				t.Errorf("goroutine %d: %v", i, err)
				return
			}
			outs[i] = inv
		}(i)
	}
	close(start)
	wg.Wait()
	for i := 1; i < N; i++ {
		if !slices.Equal(outs[i], outs[0]) {
			t.Fatalf("goroutine %d differs", i)
		}
	}
	if !slices.Equal(a, snap) {
		t.Fatal("shared input was mutated")
	}
}

// TestSelfCheck pins the built-in verification of all four invariants and
// its verdict after a rejected call.
func TestSelfCheck(t *testing.T) {
	x := New()
	if !x.SelfCheck() {
		t.Fatal("SelfCheck failed on healthy service")
	}
	if _, err := x.Inv([]float64{0, 0, 0, 0}, 2); err == nil {
		t.Fatal("expected singular error")
	}
	if !x.SelfCheck() {
		t.Fatal("SelfCheck must still pass after a rejected call")
	}
}
