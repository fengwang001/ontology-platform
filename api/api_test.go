package api_test

import (
	"errors"
	"math"
	"math/rand"
	"reflect"
	"sync"
	"testing"

	"ontology/api"
)

// Invariant 1: Indices() always has length exactly s.
func TestIndicesExactlyS(t *testing.T) {
	for _, c := range [][2]int{{1, 1}, {10, 4}, {97, 97}, {100, 10}, {1000, 7}, {10000, 9999}} {
		if sm, err := api.New(c[0], c[1], 0); err != nil || len(sm.Indices()) != c[1] {
			t.Fatalf("N=%d s=%d: err=%v", c[0], c[1], err)
		}
	}
}

// Invariant 2: every index in [0, N-1], strictly increasing. Randomized.
func TestIndicesInBoundsStrictlyIncreasing(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 300; i++ {
		n := 1 + rng.Intn(10000)
		s := 1 + rng.Intn(n)
		r := rng.Float64() * (float64(n) / float64(s)) // Float64() < 1 keeps r < d
		sm, err := api.New(n, s, r)
		if err != nil {
			t.Fatalf("N=%d s=%d r=%v: %v", n, s, r, err)
		}
		idx := sm.Indices()
		for j, ix := range idx {
			if ix < 0 || ix >= n || (j > 0 && ix <= idx[j-1]) {
				t.Fatalf("N=%d s=%d r=%v: bad index %d at %d", n, s, r, ix, j)
			}
		}
	}
}

// Invariant 3: Indices() equals the naive per-term floor(r + i*(N/s)).
func TestIndicesMatchesNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for i := 0; i < 300; i++ {
		n := 1 + rng.Intn(5000)
		s := 1 + rng.Intn(n)
		d := float64(n) / float64(s)
		r := rng.Float64() * d
		sm, err := api.New(n, s, r)
		if err != nil {
			t.Fatalf("N=%d s=%d r=%v: %v", n, s, r, err)
		}
		idx := sm.Indices()
		for j := 0; j < s; j++ {
			if want := int(math.Floor(r + float64(j)*d)); idx[j] != want {
				t.Fatalf("N=%d s=%d r=%v: idx[%d]=%d, naive %d", n, s, r, j, idx[j], want)
			}
		}
	}
}

// Invariant 4 (parameter side): invalid s / r are rejected with distinct
// decidable errors and leave existing instances untouched and usable.
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	for _, s := range []int{0, -3, 11} {
		if _, err := api.New(10, s, 0); !errors.Is(err, api.ErrSampleSize) {
			t.Fatalf("s=%d: got %v, want ErrSampleSize", s, err)
		}
	}
	for _, r := range []float64{-0.5, 2.5, 9.9} { // 2.5 == d must be rejected
		if _, err := api.New(10, 4, r); !errors.Is(err, api.ErrOffset) {
			t.Fatalf("r=%v: got %v, want ErrOffset", r, err)
		}
	}
	if api.ErrSampleSize == api.ErrOffset || api.ErrOffset == api.ErrPopulationLength || api.ErrSampleSize == api.ErrPopulationLength {
		t.Fatal("sentinel errors must be mutually distinct")
	}
	sm, err := api.New(10, 4, 1.0)
	if err != nil {
		t.Fatal(err)
	}
	before := sm.Indices()
	if _, err := sm.Sample(make([]int64, 9)); err == nil {
		t.Fatal("expected rejection")
	}
	if !reflect.DeepEqual(sm.Indices(), before) {
		t.Fatal("rejected operations mutated state")
	}
	if _, err := sm.Sample(make([]int64, 10)); err != nil {
		t.Fatalf("unusable after rejection: %v", err)
	}
}

// Invariant 4 (population side): len(vals) != N fails wholesale with its
// own decidable error; the sampler keeps working afterwards.
func TestSampleLengthMismatch(t *testing.T) {
	sm, err := api.New(10, 4, 1.0)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{0, 9, 11, 100} {
		if _, err := sm.Sample(make([]int64, n)); !errors.Is(err, api.ErrPopulationLength) {
			t.Fatalf("len=%d: got %v, want ErrPopulationLength", n, err)
		}
	}
	if out, err := sm.Sample(make([]int64, 10)); err != nil || len(out) != 4 {
		t.Fatalf("valid Sample after rejections: %v", err)
	}
}

// SelfCheck must pass on a healthy instance.
func TestSelfCheck(t *testing.T) {
	if sm, err := api.New(10, 4, 1.0); err != nil || sm.SelfCheck() != nil {
		t.Fatal("SelfCheck failed")
	}
}

// Section 6: many goroutines call Indices and SelfCheck concurrently;
// every observed index sequence must be identical. No sleeps.
func TestConcurrentIndices(t *testing.T) {
	sm, err := api.New(1000, 25, 3.75)
	if err != nil {
		t.Fatal(err)
	}
	want := sm.Indices()
	var wg sync.WaitGroup
	errs := make(chan string, 128)
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !reflect.DeepEqual(sm.Indices(), want) {
				errs <- "indices mismatch"
			}
			if err := sm.SelfCheck(); err != nil {
				errs <- err.Error()
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
}
