package api

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"sync"
	"testing"

	"ontology/solve"
)

var workA = []float64{2, 1, 1, 4, 3, 3, 8, 7, 9}

func residual(a, x, b []float64, n int) bool {
	for i := range b {
		s := 0.0
		for j := 0; j < n; j++ {
			s += a[i*n+j] * x[j]
		}
		if math.Abs(s-b[i]) > 1e-9 {
			return false
		}
	}
	return true
}

// TestSolveReuseCounter: factorCalls stays exactly 1 for m = 100/1000/10000.
func TestSolveReuseCounter(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		t.Run(fmt.Sprintf("m=%d", m), func(t *testing.T) {
			g := New()
			if err := g.Factor(workA, 3); err != nil {
				t.Fatal(err)
			}
			p := 7
			for i := 0; i < m; i++ { // distinct RHS in pseudo-random arrival order
				p = (p*1103515245 + 12345) & 0x7fffffff
				b := []float64{float64(p%11 - 5), float64((p/11)%11 - 5), float64((p/121)%11 - 5)}
				x, err := g.Solve(b, 3)
				if err != nil || !residual(workA, x, b, 3) {
					t.Fatalf("solve %d: err=%v x=%v b=%v", i, err, x, b)
				}
			}
			if c := g.factorCalls.Load(); c != 1 {
				t.Fatalf("factorCalls=%d, want 1 for m=%d", c, m)
			}
		})
	}
}

// TestConcurrentSolveIdentical: N goroutines, one RHS, byte-identical, no sleep.
func TestConcurrentSolveIdentical(t *testing.T) {
	g := New()
	if err := g.Factor(workA, 3); err != nil {
		t.Fatal(err)
	}
	b := []float64{7, 19, 49}
	const n = 64
	var wg sync.WaitGroup
	got := make([][]byte, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			x, err := g.Solve(b, 3)
			if err != nil {
				t.Errorf("solve: %v", err)
				return
			}
			got[i] = []byte(fmt.Sprintf("%v", x))
		}(i)
	}
	wg.Wait()
	for i, r := range got {
		if !bytes.Equal(r, got[0]) {
			t.Fatalf("goroutine %d got %s, want %s", i, r, got[0])
		}
	}
	if g.factorCalls.Load() != 1 {
		t.Fatalf("factorCalls=%d, want 1", g.factorCalls.Load())
	}
}

// TestRejectedOpsLeaveState pins invariant 4 and the four distinct sentinels.
func TestRejectedOpsLeaveState(t *testing.T) {
	z := New()
	calls := []func() error{
		func() error { return z.Factor(nil, 0) },
		func() error { return z.Factor([]float64{1, 2, 3}, 2) },
		func() error { return z.Factor([]float64{0, 1, 1, 0}, 2) },
		func() error { _, e := z.Solve([]float64{1}, 1); return e },
	}
	wants := []error{solve.ErrEmpty, solve.ErrDimension, solve.ErrZeroPivot, ErrNotFactored}
	for i, f := range calls {
		if err := f(); !errors.Is(err, wants[i]) {
			t.Fatalf("case %d: err=%v want %v", i, err, wants[i])
		}
	}
	if z.factorCalls.Load() != 0 || z.L != nil {
		t.Fatal("rejected calls on empty engine changed state")
	}
	g := New()
	if err := g.Factor(workA, 3); err != nil {
		t.Fatal(err)
	}
	want, _ := g.Solve([]float64{7, 19, 49}, 3)
	bad := []func() error{ // rejected after a good cache exists
		func() error { return g.Factor(nil, 0) },
		func() error { return g.Factor([]float64{1, 2, 3}, 2) },
		func() error { return g.Factor([]float64{0, 1, 1, 0}, 2) },
		func() error { _, e := g.Solve([]float64{1, 2}, 3); return e },
		func() error { _, e := g.Solve([]float64{1, 2, 3}, 2); return e },
	}
	for i, f := range bad {
		if err := f(); err == nil {
			t.Fatalf("bad case %d was accepted", i)
		}
	}
	got, err := g.Solve([]float64{7, 19, 49}, 3)
	if err != nil || !bytes.Equal([]byte(fmt.Sprintf("%v", got)), []byte(fmt.Sprintf("%v", want))) {
		t.Fatalf("cache altered by rejected calls: %v %v", got, err)
	}
	if g.factorCalls.Load() != 1 {
		t.Fatalf("factorCalls=%d, want 1", g.factorCalls.Load())
	}
	for i := 0; i < 3; i++ {
		for j := i + 1; j < 4; j++ {
			if errors.Is(wants[i], wants[j]) {
				t.Fatalf("sentinels %d and %d are not distinct", i, j)
			}
		}
	}
}

func TestSelfCheck(t *testing.T) {
	g := New()
	if err := g.Factor(workA, 3); err != nil {
		t.Fatal(err)
	}
	before, _ := g.Solve([]float64{7, 19, 49}, 3)
	if err := g.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
	after, err := g.Solve([]float64{7, 19, 49}, 3)
	if err != nil || !bytes.Equal([]byte(fmt.Sprintf("%v", after)), []byte(fmt.Sprintf("%v", before))) {
		t.Fatal("SelfCheck mutated receiver state")
	}
}
