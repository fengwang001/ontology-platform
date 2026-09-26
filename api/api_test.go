package api_test

import (
	"errors"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/api"
	"ontology/gc"
	"ontology/reg"
)

func value(t *testing.T, a *api.API, name string) int {
	t.Helper()
	v, err := a.Value(name)
	if err != nil {
		t.Fatalf("Value(%s): %v", name, err)
	}
	return v
}

// TestValueMonotonic: random Inc/MergeInto never decrease Value.
func TestValueMonotonic(t *testing.T) {
	for seed := int64(0); seed < 30; seed++ {
		rng := rand.New(rand.NewSource(seed))
		a := api.New()
		for _, n := range []string{"x", "y"} {
			if err := a.Set(n, nil); err != nil {
				t.Fatal(err)
			}
		}
		prev := map[string]int{"x": 0, "y": 0}
		for i := 0; i < 200; i++ {
			switch rng.Intn(3) {
			case 0:
				_ = a.Inc("x", rng.Intn(8), 1+rng.Intn(9))
			case 1:
				_ = a.Inc("y", rng.Intn(8), 1+rng.Intn(9))
			case 2:
				_, _ = a.MergeInto("x", "y")
			}
			for _, n := range []string{"x", "y"} {
				if v := value(t, a, n); v < prev[n] {
					t.Fatalf("seed %d iter %d: Value(%s) %d < %d", seed, i, n, v, prev[n])
				} else {
					prev[n] = v
				}
			}
		}
	}
}

// TestFailureLeavesNoTrace: the three fault classes are rejected
// with distinct sentinel errors and change no state.
func TestFailureLeavesNoTrace(t *testing.T) {
	a := api.New()
	if err := a.Set("c", map[int]int{0: 7}); err != nil {
		t.Fatal(err)
	}
	before, _ := a.Snapshot("c")
	errs := []error{
		a.Inc("c", 0, 0),                 // non-positive increment
		a.Inc("c", -1, 2),                // negative node id
		a.Set("bad", map[int]int{1: -1}), // negative entry at register
		a.Inc("missing", 0, 1),           // unknown name
	}
	want := []error{gc.ErrNonPositiveInc, gc.ErrNegativeNode, gc.ErrNegativeEntry, reg.ErrUnknownName}
	for i := range errs {
		if !errors.Is(errs[i], want[i]) {
			t.Fatalf("case %d: got %v want %v", i, errs[i], want[i])
		}
	}
	for _, sent := range want[:3] { // the three fault classes differ
		n := 0
		for _, other := range want[:3] {
			if errors.Is(sent, other) {
				n++
			}
		}
		if n != 1 {
			t.Fatalf("sentinel %v not distinct", sent)
		}
	}
	after, _ := a.Snapshot("c")
	if len(before) != len(after) || before[0] != after[0] {
		t.Fatalf("state changed: %v -> %v", before, after)
	}
	if _, err := a.Value("bad"); !errors.Is(err, reg.ErrUnknownName) {
		t.Fatal("rejected Set registered a name")
	}
	if err := a.Inc("c", 1, 1); err != nil { // still usable
		t.Fatal(err)
	}
}

// TestConcurrentInc: M goroutines Inc distinct nodes; the final
// Value equals the sum and concurrent reads never decrease.
func TestConcurrentInc(t *testing.T) {
	for _, m := range []int{8, 64, 256} {
		a := api.New()
		if err := a.Set("n", nil); err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		done := make(chan struct{})
		var wg sync.WaitGroup
		var monoErr atomic.Bool
		go func() { // reader: sampled values must be non-decreasing
			prev := 0
			for {
				select {
				case <-done:
					return
				default:
					v, err := a.Value("n")
					if err != nil || v < prev {
						monoErr.Store(true)
						return
					}
					prev = v
				}
			}
		}()
		for i := 0; i < m; i++ {
			wg.Add(1)
			go func(node int) {
				defer wg.Done()
				<-start
				_ = a.Inc("n", node, 2)
			}(i)
		}
		close(start)
		wg.Wait()
		close(done)
		if monoErr.Load() {
			t.Fatalf("m=%d: concurrent Value decreased", m)
		}
		if got := value(t, a, "n"); got != m*2 {
			t.Fatalf("m=%d: Value=%d want %d", m, got, m*2)
		}
	}
}

func TestSelfCheck(t *testing.T) {
	if err := api.New().SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
