package ema

import (
	"errors"
	"math"
	"math/rand"
	"testing"
)

func oracle(h []float64, a float64) (float64, bool) {
	if len(h) == 0 {
		return 0, false
	}
	m := h[0]
	for _, x := range h[1:] {
		m = math.FMA(a, x, (1-a)*m)
	}
	return m, true
}
func findLast(h []float64, v float64) int {
	for i := len(h) - 1; i >= 0; i-- {
		if h[i] == v {
			return i
		}
	}
	return -1
}
func mustNew(a float64) *EWMA { e, _ := New(a); return e }

func TestInvariantBatchRecompute(t *testing.T) {
	seq := []float64{10, 20, 10, 30}
	for _, a := range []float64{0.25, 1, 0.1} {
		e := mustNew(a)
		for _, v := range seq {
			e.Add(v)
		}
		if w, _ := oracle(seq, a); e.Value() != w {
			t.Errorf("a=%v: Value=%v want %v", a, e.Value(), w)
		}
	}
	rng := rand.New(rand.NewSource(473))
	for tr := 0; tr < 150; tr++ {
		a := math.Min(1, 0.1+rng.Float64()*0.9)
		e, m := mustNew(a), []float64(nil)
		for op := 0; op < 50; op++ {
			v := float64(rng.Intn(4))
			if idx := findLast(m, v); rng.Intn(3) == 0 && len(m) > 0 {
				err := e.Retract(v)
				if idx >= 0 {
					if err != nil {
						t.Fatalf("unexpected err: %v", err)
					}
					m = append(m[:idx], m[idx+1:]...)
				} else if !errors.Is(err, ErrRetractNotFound) {
					t.Fatalf("want not-found, got %v", err)
				}
			} else {
				e.Add(v)
				m = append(m, v)
			}
			if w, init := oracle(m, a); e.Initialized() != init || init && e.Value() != w {
				t.Fatalf("tr %d op %d: Value=%v model=%v want %v", tr, op, e.Value(), m, w)
			}
		}
	}
}
func TestInvariantInitialization(t *testing.T) {
	for _, tc := range []struct{ a, v float64 }{
		{0.25, 10}, {1, 7}, {0.5, -3}, {0.9, 0}} {
		e := mustNew(tc.a)
		if e.Initialized() {
			t.Fatalf("a=%v: empty reports initialized", tc.a)
		}
		e.Add(tc.v)
		if !e.Initialized() || e.Value() != tc.v {
			t.Errorf("a=%v v=%v: first EWMA=%v want %v", tc.a, tc.v, e.Value(), tc.v)
		}
		if tc.a != 1 && tc.v != 0 && e.Value() == tc.a*tc.v {
			t.Errorf("a=%v v=%v: zero-seed bias", tc.a, tc.v)
		}
	}
}
func TestInvariantRetractExact(t *testing.T) {
	e := mustNew(0.25)
	for _, v := range []float64{10, 20, 10, 30} {
		e.Add(v)
	}
	if err := e.Retract(10); err != nil || e.Value() != 16.875 {
		t.Fatalf("latest retract (index 2, not 0 -> 20.625): v=%v err=%v", e.Value(), err)
	}
	for _, tc := range []struct {
		b []float64
		x float64
	}{{[]float64{1, 2, 3}, 99}, {[]float64{10}, 20}, {[]float64{-5, 0, 5, -5}, 7}} {
		a, b := mustNew(0.4), mustNew(0.4)
		for _, v := range tc.b {
			a.Add(v)
			b.Add(v)
		}
		a.Add(tc.x)
		if err := a.Retract(tc.x); err != nil || a.Value() != b.Value() {
			t.Errorf("retract not exact: %v vs %v err=%v", a.Value(), b.Value(), err)
		}
	}
	g := mustNew(0.25)
	g.Add(1)
	g.Add(2)
	if err := g.Retract(2); err != nil || g.Value() != 1 {
		t.Fatal("retract 2 failed")
	}
	if err := g.Retract(1); err != nil || g.Initialized() {
		t.Fatalf("not undefined after draining: err=%v init=%v", err, g.Initialized())
	}
}
func TestInvariantFailureLeavesNoTrace(t *testing.T) {
	for _, a := range []float64{0, -0.5, 1.0001, math.NaN()} {
		if e, err := New(a); !errors.Is(err, ErrInvalidAlpha) || e != nil {
			t.Errorf("a=%v: want ErrInvalidAlpha, got %v", a, err)
		}
	}
	e := mustNew(0.25)
	if err := e.Retract(1); !errors.Is(err, ErrRetractEmpty) {
		t.Errorf("empty retract: %v", err)
	}
	e.Add(7)
	sE, sN := e.Value(), len(e.hist)
	if err := e.Retract(9); !errors.Is(err, ErrRetractNotFound) {
		t.Errorf("missing retract: %v", err)
	}
	if e.Value() != sE || len(e.hist) != sN {
		t.Fatal("rejected Retract mutated state")
	}
	if errors.Is(ErrInvalidAlpha, ErrRetractEmpty) || errors.Is(ErrRetractEmpty, ErrRetractNotFound) ||
		errors.Is(ErrInvalidAlpha, ErrRetractNotFound) {
		t.Fatal("sentinel errors not distinct")
	}
}
func TestAddConstantTime(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		e := mustNew(0.25)
		for i := 0; i < m; i++ {
			e.Add(float64(i))
		}
		e.Add(42)
		if e.lastAddChecked > addCheckBound {
			t.Errorf("m=%d: inspected %d, bound %d", m, e.lastAddChecked, addCheckBound)
		}
	}
}
