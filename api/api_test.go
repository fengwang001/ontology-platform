package api

import (
	"errors"
	"math"
	"math/rand"
	"sync"
	"testing"
)

func refClosed(alpha, seed float64, xs []float64, bc bool) float64 {
	beta, n := 1-alpha, float64(len(xs))
	s := math.Pow(beta, n) * seed
	for i, x := range xs {
		s += alpha * math.Pow(beta, float64(len(xs)-1-i)) * x
	}
	if bc {
		s /= 1 - math.Pow(beta, n)
	}
	return s
}

// TestClosedForm pins invariant 3: recurrence == batch closed form.
func TestClosedForm(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	cases := []struct {
		alpha, seed float64
		bc          bool
	}{
		{0.1, 0, false}, {0.5, 3, false}, {0.9, -7, false},
		{0.1, 0, true}, {0.5, 0, true}, {0.9, 0, true},
	}
	for _, tc := range cases {
		a, err := New(tc.alpha, tc.seed, tc.bc)
		if err != nil {
			t.Fatal(err)
		}
		xs := []float64{}
		for i := 0; i < 60; i++ {
			x := rng.NormFloat64() * 30
			xs = append(xs, x)
			if err := a.Update("k", x); err != nil {
				t.Fatal(err)
			}
			got, _ := a.Value("k")
			if want := refClosed(tc.alpha, tc.seed, xs, tc.bc); math.Abs(got-want) > 1e-8*math.Max(1, math.Abs(want)) {
				t.Fatalf("alpha=%v bc=%v step %d: %v != %v", tc.alpha, tc.bc, i, got, want)
			}
		}
	}
}

// TestRejectedOperationsLeaveNoTrace pins invariant 4: distinct
// sentinels, nothing mutated, object still usable afterwards.
func TestRejectedOperationsLeaveNoTrace(t *testing.T) {
	if errors.Is(ErrInvalidAlpha, ErrEmptyKey) || errors.Is(ErrEmptyKey, ErrNotFound) || errors.Is(ErrInvalidAlpha, ErrNotFound) {
		t.Fatal("sentinel errors must be pairwise distinct")
	}
	for _, alpha := range []float64{0, 1, -0.5, 2} {
		a, err := New(alpha, 0, false)
		if !errors.Is(err, ErrInvalidAlpha) || a != nil {
			t.Fatalf("alpha %v must fail with ErrInvalidAlpha and nil API", alpha)
		}
	}
	a, _ := New(0.9, 0, false)
	if err := a.Update("keep", 5); err != nil {
		t.Fatal(err)
	}
	before, _ := a.Value("keep")
	if err := a.Update("", 9); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("empty Update: %v", err)
	}
	if _, err := a.Value(""); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("empty Value: %v", err)
	}
	if _, err := a.Value("ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown key: %v", err)
	}
	if after, _ := a.Value("keep"); after != before || a.Count("") != 0 || a.Count("ghost") != 0 {
		t.Fatal("rejected operation changed state")
	}
	if err := a.Update("keep", 7); err != nil { // still usable
		t.Fatal(err)
	}
}

// TestFiveSteps checks the NOTES.md worked example for [10,20,10,20,10].
func TestFiveSteps(t *testing.T) {
	a, _ := New(0.9, 0, false)
	want := []float64{9, 18.9, 10.89, 19.089, 10.9089}
	for i, x := range []float64{10, 20, 10, 20, 10} {
		if err := a.Update("k", x); err != nil {
			t.Fatal(err)
		}
		got, _ := a.Value("k")
		if math.Abs(got-want[i]) > 1e-10 {
			t.Fatalf("step %d: got %v want %v", i+1, got, want[i])
		}
	}
}

// TestConcurrentValueConsistency: concurrent readers of settled keys
// all observe identical per-key values (run under -race).
func TestConcurrentValueConsistency(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	a, _ := New(0.3, 1, true)
	const keys, per, readers = 24, 15, 16
	for k := 0; k < keys; k++ {
		for i := 0; i < per; i++ {
			if err := a.Update(string(rune('a'+k)), rng.NormFloat64()); err != nil {
				t.Fatal(err)
			}
		}
	}
	start := make(chan struct{})
	res := make([][]float64, readers)
	var wg sync.WaitGroup
	for g := 0; g < readers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-start
			row := make([]float64, keys)
			for k := range row {
				v, err := a.Value(string(rune('a' + k)))
				if err != nil {
					t.Error(err)
				}
				row[k] = v
			}
			res[g] = row
		}(g)
	}
	close(start)
	wg.Wait()
	for k := 0; k < keys; k++ {
		for g := 1; g < readers; g++ {
			if res[g][k] != res[0][k] {
				t.Fatalf("key %c: reader %d got %v, reader 0 got %v", 'a'+k, g, res[g][k], res[0][k])
			}
		}
	}
}

func TestSelfCheck(t *testing.T) {
	a, _ := New(0.4, 0, true)
	if err := a.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
