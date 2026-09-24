package bidx

import (
	"errors"
	"math"
	"math/rand"
	"testing"
)

func naive(ts []int64, T int64) (int64, bool) {
	run, res, found := int64(0), int64(-1), false
	for o, v := range ts {
		if o == 0 || v > run {
			run = v
		}
		if run <= T {
			res, found = int64(o), true
		}
	}
	return res, found
}

func TestAppendForwardAndPM(t *testing.T) {
	cases := []struct {
		name string
		ts   []int64
		want []int64
	}{
		{"spec8", []int64{2, 1, 8, 3, 4, 9, 5, 7}, []int64{2, 2, 8, 8, 8, 9, 9, 9}},
		{"neg", []int64{-5, -9, -1, -2}, []int64{-5, -5, -1, -1}},
		{"dup", []int64{3, 3, 3}, []int64{3, 3, 3}},
		{"minmax", []int64{math.MaxInt64, math.MinInt64}, []int64{math.MaxInt64, math.MaxInt64}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			x := New()
			for i, v := range c.ts {
				x.Append(v)
				if got, _ := x.TSAt(int64(i)); got != v { // invariant 1
					t.Fatalf("TSAt(%d)=%d want %d", i, got, v)
				}
			}
			for i, want := range c.want {
				if x.pm[i] != want {
					t.Fatalf("pm[%d]=%d want %d", i, x.pm[i], want)
				}
			}
		})
	}
}

// TestPrefixMaxMonotonic pins invariant 3 across random arrivals:
// incrementally maintained pm must never decrease.
func TestPrefixMaxMonotonic(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for _, n := range []int{1, 8, 100, 1000} {
		x := New()
		for i := 0; i < n; i++ {
			x.Append(r.Int63n(100) - 50)
			if i > 0 && x.pm[i] < x.pm[i-1] {
				t.Fatalf("n=%d pm decreased at %d: %d < %d", n, i, x.pm[i], x.pm[i-1])
			}
		}
	}
}

func TestSafeOffFixed(t *testing.T) {
	x := New()
	for _, v := range []int64{2, 1, 8, 3, 4, 9, 5, 7} {
		x.Append(v)
	}
	cases := []struct {
		T     int64
		off   int64
		found bool
	}{
		{7, 1, true}, {8, 4, true}, {2, 1, true},
		{1, -1, false}, {9, 7, true}, {100, 7, true},
	}
	for _, c := range cases {
		off, found, err := x.SafeOff(c.T)
		if err != nil || off != c.off || found != c.found {
			t.Fatalf("SafeOff(%d)=(%d,%v,%v) want (%d,%v)", c.T, off, found, err, c.off, c.found)
		}
	}
}

// TestSafeOffNaive fuzzes several sizes: binary SafeOff must match the
// naive linear scan value-for-value, and the comparison count must stay
// within ceil(log2(N+1))+2 instead of growing linearly with N.
func TestSafeOffNaive(t *testing.T) {
	r := rand.New(rand.NewSource(408))
	sizes := []int{100, 333, 1000, 3333, 10000}
	for _, n := range sizes {
		ts := make([]int64, n)
		for i := range ts {
			ts[i] = r.Int63n(1000) - 500
		}
		x := New()
		for _, v := range ts {
			x.Append(v)
		}
		bound := 0
		for b := 1; b < n+1; b <<= 1 {
			bound++
		}
		bound += 2 // ceil(log2(N+1)) + small constant
		for k := 0; k < 32; k++ {
			T := r.Int63n(2000) - 1000
			off, found, err := x.SafeOff(T)
			if err != nil {
				t.Fatal(err)
			}
			w, wf := naive(ts, T)
			if off != w || found != wf {
				t.Fatalf("n=%d T=%d got (%d,%v) naive (%d,%v)", n, T, off, found, w, wf)
			}
			if x.cmp.Load() > int64(bound) {
				t.Fatalf("n=%d comparisons=%d > bound %d (linear scan?)", n, x.cmp.Load(), bound)
			}
		}
		if x.cmp.Load() > int64(bound) { // the single final SafeOff of this size
			t.Fatalf("final comparisons %d > %d", x.cmp.Load(), bound)
		}
	}
}

func TestSentinelsAndNoTrace(t *testing.T) {
	x := New()
	if _, _, err := x.SafeOff(0); !errors.Is(err, ErrEmptyLog) {
		t.Fatalf("empty: %v", err)
	}
	x.Append(5)
	if _, err := x.TSAt(-1); !errors.Is(err, ErrOutOfRange) {
		t.Fatalf("negative off: %v", err)
	}
	if _, err := x.TSAt(1); !errors.Is(err, ErrOutOfRange) {
		t.Fatalf("past-end off: %v", err)
	}
	if x.Len() != 1 { // rejected reads changed nothing
		t.Fatalf("len=%d want 1", x.Len())
	}
	if ErrEmptyLog == ErrOutOfRange {
		t.Fatal("sentinel errors must be distinct")
	}
}
