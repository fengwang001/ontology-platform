package api_test

import (
	"errors"
	"math"
	"math/rand"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/api"
)

// TestNaiveConsistency pins invariant 1: after every op the window contents
// equal a naive FIFO and Max equals its scanned maximum. Table-driven over
// capacities, seeds and lengths.
func TestNaiveConsistency(t *testing.T) {
	cases := []struct{ W, n, seed int }{
		{1, 500, 1}, {2, 500, 2}, {4, 1000, 3}, {8, 2000, 4}, {64, 5000, 5},
	}
	for _, c := range cases {
		w, err := api.New(c.W)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		r := rand.New(rand.NewSource(int64(c.seed)))
		var ref []float64
		for n := 0; n < c.n; n++ {
			if r.Intn(3) == 0 && len(ref) > 0 {
				got, e := w.Evict()
				if e != nil || got != ref[0] {
					t.Fatalf("c%+v step %d: evict mismatch", c, n)
				}
				ref = ref[1:]
			} else {
				v := float64(r.Intn(5)) // many tied maxima
				if e := w.Push(v); e != nil {
					t.Fatalf("push: %v", e)
				}
				ref = append(ref, v)
				if len(ref) > c.W {
					ref = ref[1:]
				}
			}
			vs, m, e := w.Snapshot()
			if len(ref) == 0 {
				continue
			}
			if e != nil || !slices.Equal(vs, ref) || m != slices.Max(ref) {
				t.Fatalf("c%+v step %d: vs=%v ref=%v m=%v e=%v", c, n, vs, ref, m, e)
			}
		}
	}
}

// TestRejectedAtomicity pins invariant 4: the three sentinel errors are
// distinct and decidable, and every rejected op leaves state unchanged; the
// window stays usable.
func TestRejectedAtomicity(t *testing.T) {
	if api.ErrCapacity == api.ErrEmpty || api.ErrCapacity == api.ErrNaN || api.ErrEmpty == api.ErrNaN {
		t.Fatal("sentinel errors must be distinct")
	}
	bad := []int{0, -1, -100}
	for _, W := range bad {
		if w, e := api.New(W); w != nil || !errors.Is(e, api.ErrCapacity) {
			t.Fatalf("New(%d) should fail with ErrCapacity", W)
		}
	}
	empty, _ := api.New(3)
	if _, e := empty.Evict(); !errors.Is(e, api.ErrEmpty) {
		t.Fatal("empty Evict must be ErrEmpty")
	}
	if _, e := empty.Max(); !errors.Is(e, api.ErrEmpty) {
		t.Fatal("empty Max must be ErrEmpty")
	}
	if _, _, e := empty.Snapshot(); !errors.Is(e, api.ErrEmpty) {
		t.Fatal("empty Snapshot must be ErrEmpty")
	}
	// NaN rejected on Push and on PushAll (whole batch), state untouched.
	w, _ := api.New(4)
	if e := w.PushAll([]float64{5, 1, 5}); e != nil {
		t.Fatal(e)
	}
	before, bm, _ := w.Snapshot()
	if e := w.Push(math.NaN()); !errors.Is(e, api.ErrNaN) {
		t.Fatalf("Push NaN: %v", e)
	}
	for _, batch := range [][]float64{{math.NaN()}, {1, math.NaN()}, {math.NaN(), 9}, {1, math.NaN(), 2}} {
		if e := w.PushAll(batch); !errors.Is(e, api.ErrNaN) {
			t.Fatalf("PushAll %v: %v", batch, e)
		}
	}
	after, am, e := w.Snapshot()
	if e != nil || am != bm || !slices.Equal(before, after) {
		t.Fatalf("rejected op changed state: before=%v after=%v", before, after)
	}
	if e := w.Push(9); e != nil {
		t.Fatalf("window unusable after rejection: %v", e)
	}
	if m, _ := w.Max(); m != 9 {
		t.Fatalf("Max after recovery = %v, want 9", m)
	}
}

// TestConcurrentSnapshots: one writer pushes random values (with ties) and
// evicts, while N readers repeatedly Snapshot. Each snapshot's Max must equal
// the scan of its own contents and its length must be <= W. No sleeps.
func TestConcurrentSnapshots(t *testing.T) {
	const W = 16
	w, _ := api.New(W)
	var wg sync.WaitGroup
	var done, bad atomic.Bool
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !done.Load() {
				vs, m, e := w.Snapshot()
				if e != nil {
					continue // empty window is a valid observation
				}
				if len(vs) > W || m != slices.Max(vs) {
					bad.Store(true)
					return
				}
			}
		}()
	}
	r := rand.New(rand.NewSource(7))
	for i := 0; i < 100000; i++ {
		if e := w.Push(float64(r.Intn(6))); e != nil {
			t.Fatal(e)
		}
		if i%3 == 0 {
			_, _ = w.Evict()
		}
	}
	done.Store(true)
	wg.Wait()
	if bad.Load() {
		t.Fatal("inconsistent concurrent snapshot observed")
	}
}
