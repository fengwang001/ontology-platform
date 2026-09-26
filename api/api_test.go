package api_test

import (
	"errors"
	"math"
	"reflect"
	"sync"
	"testing"

	"ontology/api"
)

// TestExactCount pins invariant 1: Indices() always has exactly s entries.
func TestExactCount(t *testing.T) {
	for _, c := range []struct {
		n, s int
		r    float64
	}{
		{10, 4, 1}, {10, 1, 0}, {10, 10, 0}, {7, 3, 0.5}, {1000, 37, 2},
	} {
		z, err := api.New(c.n, c.s, c.r)
		if err != nil {
			t.Fatalf("n=%d s=%d r=%v: %v", c.n, c.s, c.r, err)
		}
		if got := len(z.Indices()); got != c.s {
			t.Errorf("n=%d s=%d: got %d indices, want %d", c.n, c.s, got, c.s)
		}
	}
}

// TestBoundsAndStrictIncrease pins invariant 2 over loop-generated cases.
func TestBoundsAndStrictIncrease(t *testing.T) {
	for n := 1; n <= 30; n++ {
		for s := 1; s <= n; s++ {
			d := float64(n) / float64(s)
			for _, frac := range []float64{0, 0.25, 0.5, 0.99} {
				z, err := api.New(n, s, d*frac)
				if err != nil {
					t.Fatalf("n=%d s=%d r=%v: %v", n, s, d*frac, err)
				}
				idx := z.Indices()
				if idx[0] < 0 || idx[s-1] >= n {
					t.Fatalf("n=%d s=%d: out of bounds %v", n, s, idx)
				}
				for i := 1; i < s; i++ {
					if idx[i] <= idx[i-1] {
						t.Fatalf("n=%d s=%d: not strictly increasing %v", n, s, idx)
					}
				}
			}
		}
	}
}

// TestNaiveReference pins invariant 3, including canonical [1 3 6 8].
func TestNaiveReference(t *testing.T) {
	for _, c := range []struct {
		n, s int
		r    float64
	}{
		{10, 4, 1}, {7, 3, 0.5}, {100, 10, 0},
	} {
		z, err := api.New(c.n, c.s, c.r)
		if err != nil {
			t.Fatalf("n=%d s=%d: %v", c.n, c.s, err)
		}
		got := z.Indices()
		d := float64(c.n) / float64(c.s)
		for i := 0; i < c.s; i++ {
			if want := int(math.Floor(c.r + float64(i)*d)); got[i] != want {
				t.Errorf("n=%d s=%d r=%v i=%d: got %d want %d", c.n, c.s, c.r, i, got[i], want)
			}
		}
	}
	z, _ := api.New(10, 4, 1)
	if !reflect.DeepEqual(z.Indices(), []int{1, 3, 6, 8}) {
		t.Fatalf("canonical: got %v want [1 3 6 8]", z.Indices())
	}
}

// TestRejectedOpsLeaveNoTrace pins invariant 4 and the three distinct errors.
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	if errors.Is(api.ErrInvalidSize, api.ErrOffsetOutOfRange) ||
		errors.Is(api.ErrInvalidSize, api.ErrPopulationLengthMismatch) ||
		errors.Is(api.ErrOffsetOutOfRange, api.ErrPopulationLengthMismatch) {
		t.Fatal("the three sentinel errors must be distinct")
	}
	for _, b := range []struct {
		n, s int
		r    float64
		want error
	}{
		{10, 0, 0, api.ErrInvalidSize},
		{10, 11, 0, api.ErrInvalidSize},
		{10, 4, -0.01, api.ErrOffsetOutOfRange},
		{10, 4, 2.5, api.ErrOffsetOutOfRange},
	} {
		if _, e := api.New(b.n, b.s, b.r); !errors.Is(e, b.want) {
			t.Errorf("n=%d s=%d r=%v: got %v want %v", b.n, b.s, b.r, e, b.want)
		}
	}
	z, _ := api.New(10, 4, 1)
	pop := []int64{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}
	before := z.Indices()
	sampleBefore, err := z.Sample(pop)
	if err != nil {
		t.Fatal(err)
	}
	if _, e := z.Sample(make([]int64, 9)); !errors.Is(e, api.ErrPopulationLengthMismatch) {
		t.Errorf("len mismatch: got %v", e)
	}
	if got := z.Indices(); !reflect.DeepEqual(got, before) {
		t.Errorf("indices changed after rejection: %v vs %v", got, before)
	}
	if after, e := z.Sample(pop); e != nil || !reflect.DeepEqual(after, sampleBefore) {
		t.Errorf("sampler not reusable after rejection: %v %v", after, e)
	}
}

// TestConcurrentIndicesIdentical: goroutines on one instance get bit-identical
// indices while SelfCheck runs concurrently. No sleeps.
func TestConcurrentIndicesIdentical(t *testing.T) {
	z, err := api.New(1000, 64, 3)
	if err != nil || !z.SelfCheck() {
		t.Fatalf("construction/selfcheck: %v", err)
	}
	want := z.Indices()
	const g = 200
	var wg sync.WaitGroup
	var mu sync.Mutex
	bad := 0
	mark := func() { mu.Lock(); bad++; mu.Unlock() }
	for k := 0; k < g; k++ {
		wg.Add(1)
		go func(checkSelf bool) {
			defer wg.Done()
			if checkSelf && !z.SelfCheck() {
				mark()
			}
			if !reflect.DeepEqual(z.Indices(), want) {
				mark()
			}
		}(k%5 == 0)
	}
	wg.Wait()
	if bad != 0 {
		t.Fatalf("%d goroutines saw differing indices or a SelfCheck failure", bad)
	}
}
