package sparse

import (
	"math"
	"sync"
	"testing"
)

func snapshot(v Vector) []uint64 {
	s := make([]uint64, len(v)*2)
	for i, e := range v {
		s[2*i] = uint64(e.Index)
		s[2*i+1] = math.Float64bits(e.Value)
	}
	return s
}

// Computation must never mutate its inputs (no in-place sorting, nothing).
func TestInputsNotModified(t *testing.T) {
	a := Vector{{0, 1.5}, {3, -2}, {9, 0}, {100, 4}}
	b := Vector{{1, 1}, {3, 2}, {9, 0}, {100, -1}}
	ba, bb := snapshot(a), snapshot(b)
	if _, _, err := Dot(a, b); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Cosine(a, b); err != nil {
		t.Fatal(err)
	}
	for i := range ba {
		if snapshot(a)[i] != ba[i] || snapshot(b)[i] != bb[i] {
			t.Fatal("input vector was modified")
		}
	}
}

// The same pair recomputed any number of times is bitwise identical.
func TestRepeatedComputationDeterministic(t *testing.T) {
	a := Vector{{0, 1e16}, {1, 1}, {2, -1e16}, {5, 0.1}}
	b := Vector{{0, 1}, {1, 1}, {2, 1}, {5, math.Pi}}
	d0, s0, err := Dot(a, b)
	if err != nil {
		t.Fatal(err)
	}
	c0, _, err := Cosine(a, b)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1000; i++ {
		d, s, _ := Dot(a, b)
		c, _, _ := Cosine(a, b)
		if math.Float64bits(d) != math.Float64bits(d0) || s != s0 ||
			math.Float64bits(c) != math.Float64bits(c0) {
			t.Fatalf("iteration %d diverged", i)
		}
	}
}

// Concurrent computations on disjoint pairs: results and per-call step
// counts must not bleed across goroutines. Run with -race.
func TestConcurrentStatsIsolation(t *testing.T) {
	const workers = 16
	const iters = 200
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			// Each worker owns a pair with a distinct element count, so its
			// expected step count is unique and any cross-talk is visible.
			n := w + 2
			a := make(Vector, n)
			b := make(Vector, n)
			for i := 0; i < n; i++ {
				a[i] = Element{uint32(2 * i), float64(w + i)}
				b[i] = Element{uint32(2*i + 1), float64(w - i)}
			}
			wantSteps := uint64(2*n - 1) // fully interleaved merge
			var wantDot float64
			for it := 0; it < iters; it++ {
				d, st, err := Dot(a, b)
				if err != nil {
					t.Error(err)
					return
				}
				if st.Steps != wantSteps {
					t.Errorf("worker %d: steps %d, want %d",
						w, st.Steps, wantSteps)
					return
				}
				if d != 0 {
					t.Errorf("worker %d: disjoint indices, dot %v != 0", w, d)
					return
				}
				if it == 0 {
					wantDot = d
				} else if math.Float64bits(d) != math.Float64bits(wantDot) {
					t.Errorf("worker %d: non-deterministic result", w)
					return
				}
			}
		}(w)
	}
	wg.Wait()
}
