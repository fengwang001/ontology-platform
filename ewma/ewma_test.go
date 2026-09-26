package ewma

import (
	"math"
	"math/rand"
	"testing"
)

func approxEq(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9*math.Max(1, math.Abs(want)) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// TestRangeInvariant pins invariant 1: after every update the value lies
// in [min(seed, observations), max(seed, observations)].
func TestRangeInvariant(t *testing.T) {
	rng := rand.New(rand.NewSource(742))
	cases := []struct {
		name string
		bc   bool
	}{{"no-correction", false}, {"correction-seed0", true}}
	for _, tc := range cases {
		for _, alpha := range []float64{0.05, 0.5, 0.9} {
			t.Run(tc.name, func(t *testing.T) {
				seed := 0.0
				if !tc.bc { // corrected range claim uses seed 0 (see api.SelfCheck)
					seed = rng.NormFloat64() * 20
				}
				e, err := New(alpha, seed, tc.bc)
				if err != nil {
					t.Fatal(err)
				}
				lo, hi := seed, seed
				for i := 0; i < 200; i++ {
					x := rng.NormFloat64() * 100
					lo, hi = math.Min(lo, x), math.Max(hi, x)
					e.Update(x)
					v := e.Value()
					if v < lo-1e-9 || v > hi+1e-9 {
						t.Fatalf("step %d: %v outside [%v,%v]", i, v, lo, hi)
					}
				}
			})
		}
	}
}

// TestConstantInput pins invariant 2: constant observations c with
// seed 0 give exactly c with correction, and c*(1-beta^n) without.
func TestConstantInput(t *testing.T) {
	cases := []struct {
		alpha, c float64
		bc       bool
	}{
		{0.9, 5, true}, {0.5, -3, true}, {0.1, 42, true},
		{0.9, 5, false}, {0.5, -3, false}, {0.1, 42, false},
	}
	for _, tc := range cases {
		e, _ := New(tc.alpha, 0, tc.bc)
		beta := 1 - tc.alpha
		betaPow := 1.0
		for n := 1; n <= 50; n++ {
			betaPow *= beta
			e.Update(tc.c)
			want := tc.c * (1 - betaPow)
			if tc.bc {
				want = tc.c
			}
			approxEq(t, e.Value(), want)
		}
	}
}

// TestValueReadsExactlyOneHistoryObservation pins the O(1) complexity
// claim: regardless of how many observations were folded in, one Value
// call reads exactly one retained piece of history (the state s).
func TestValueReadsExactlyOneHistoryObservation(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		e, _ := New(0.3, 0, true)
		for i := 0; i < m; i++ {
			e.Update(float64(i % 7))
		}
		e.reads.Store(0)
		_ = e.Value()
		if got := e.reads.Load(); got != 1 {
			t.Fatalf("m=%d: Value read %d historical observations, want exactly 1", m, got)
		}
	}
}

func TestNewRejectsAlpha(t *testing.T) {
	for _, alpha := range []float64{-1, 0, 1, 2} {
		if _, err := New(alpha, 0, false); err != ErrInvalidAlpha {
			t.Fatalf("alpha %v: got %v, want ErrInvalidAlpha", alpha, err)
		}
	}
}
