package ema

import (
	"errors"
	"math/rand"
	"testing"
)

// batch recomputes the EWMA from scratch over hist.
func batch(alpha float64, hist []float64) float64 {
	v := hist[0]
	for _, x := range hist[1:] {
		v = alpha*x + (1-alpha)*v
	}
	return v
}

// close tolerates last-ulp drift from fused multiply-add differences.
func close(got, want float64) bool {
	d := got - want
	if d < 0 {
		d = -d
	}
	if want < 0 {
		want = -want
	}
	if want < 1 {
		want = 1
	}
	return d <= 1e-12*want
}

// feed returns an EMA with vs added in order.
func feed(alpha float64, vs ...float64) *EMA {
	e, _ := New(alpha)
	for _, v := range vs {
		e.Add(v)
	}
	return e
}

// Invariant 2: empty is uninitialized; first Add seeds directly.
func TestFirstAddSeedsDirectly(t *testing.T) {
	for _, alpha := range []float64{0.1, 0.25, 0.5, 1} {
		e, _ := New(alpha)
		if e.Initialized() {
			t.Fatalf("alpha=%v: empty sequence initialized", alpha)
		}
		e.Add(10)
		if !e.Initialized() || e.Value() != 10 {
			t.Fatalf("alpha=%v: got %v, want 10 (zero-seed: %v)", alpha, e.Value(), alpha*10)
		}
	}
}

// Invariant 1: any Add/Retract sequence matches batch recompute.
func TestMatchesBatchRecompute(t *testing.T) {
	for _, alpha := range []float64{0.05, 0.25, 0.7, 1} {
		rng := rand.New(rand.NewSource(int64(alpha * 1000)))
		for trial := 0; trial < 10; trial++ {
			e, _ := New(alpha)
			var hist []float64
			for step := 0; step < 150; step++ {
				if len(hist) == 0 || rng.Intn(3) > 0 {
					v := float64(rng.Intn(8)) // small pool: retracts hit
					e.Add(v)
					hist = append(hist, v)
				} else {
					v := hist[rng.Intn(len(hist))]
					if err := e.Retract(v); err != nil {
						t.Fatal(err)
					}
					i := len(hist) - 1
					for hist[i] != v {
						i--
					}
					hist = append(hist[:i], hist[i+1:]...)
				}
				if (len(hist) == 0) == e.Initialized() {
					t.Fatalf("alpha=%v step=%d: init=%v len=%d", alpha, step, e.Initialized(), len(hist))
				}
				if len(hist) > 0 && !close(e.Value(), batch(alpha, hist)) {
					t.Fatalf("alpha=%v step=%d: got %v want %v", alpha, step, e.Value(), batch(alpha, hist))
				}
			}
		}
	}
}

// Invariant 3: retracting v equals v never having been added.
func TestRetractExactEquivalence(t *testing.T) {
	cases := []struct {
		alpha, retract float64
		seq, kept      []float64 // kept = seq minus most recent occurrence
	}{
		{alpha: 0.25, seq: []float64{10, 20, 10, 30}, retract: 10, kept: []float64{10, 20, 30}},
		{alpha: 0.25, seq: []float64{10, 20, 10, 30}, retract: 20, kept: []float64{10, 10, 30}},
		{alpha: 0.5, seq: []float64{5, 5, 5}, retract: 5, kept: []float64{5, 5}},
		{alpha: 1, seq: []float64{3, 1, 4, 1, 5}, retract: 1, kept: []float64{3, 1, 4, 5}},
		{alpha: 0.25, seq: []float64{42}, retract: 42, kept: nil},
	}
	for _, c := range cases {
		a, b := feed(c.alpha, c.seq...), feed(c.alpha, c.kept...)
		if err := a.Retract(c.retract); err != nil {
			t.Fatal(err)
		}
		if a.Initialized() != b.Initialized() || (len(c.kept) > 0 && a.Value() != b.Value()) {
			t.Fatalf("%+v: got (%v,%v) want (%v,%v)", c, a.Initialized(), a.Value(), b.Initialized(), b.Value())
		}
	}
}

// Invariant 4: rejected operations change nothing.
func TestFailureLeavesNoTrace(t *testing.T) {
	for _, alpha := range []float64{0, -1, 1.5} {
		if _, err := New(alpha); !errors.Is(err, ErrInvalidAlpha) {
			t.Fatalf("alpha=%v: %v", alpha, err)
		}
	}
	if e, _ := New(0.25); e.Retract(1) != ErrEmpty || e.Initialized() || e.Len() != 0 {
		t.Fatal("empty retract did not fail cleanly")
	}
	e := feed(0.25, 10, 20, 10)
	before, n := e.Value(), e.Len()
	if err := e.Retract(99); !errors.Is(err, ErrNotFound) || e.Value() != before || e.Len() != n {
		t.Fatal("failed retract mutated state")
	}
	if errors.Is(ErrInvalidAlpha, ErrNotFound) || errors.Is(ErrNotFound, ErrEmpty) || errors.Is(ErrEmpty, ErrInvalidAlpha) {
		t.Fatal("sentinel errors not distinct")
	}
	e.Add(30) // still usable afterwards
	if got, want := e.Value(), batch(0.25, []float64{10, 20, 10, 30}); got != want {
		t.Fatalf("after reject: got %v want %v", got, want)
	}
}

// Complexity: Add inspects O(1) hist elements regardless of hist size.
func TestAddChecksBounded(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		e, _ := New(0.25)
		for i := 0; i < m; i++ {
			e.Add(float64(i))
		}
		e.Add(-1)
		if e.lastAddChecked > 1 {
			t.Fatalf("m=%d: Add checked %d hist elements, want <=1", m, e.lastAddChecked)
		}
	}
}
