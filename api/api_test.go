package api

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"ontology/orset"
)

func TestNaiveReference(t *testing.T) {
	for _, seed := range []int64{1, 7, 42, 296, 2026} {
		if err := checkNaive(seed); err != nil {
			t.Errorf("seed %d: %v", seed, err)
		}
	}
}

func TestSelfCheck(t *testing.T) {
	a, _ := New(2, 100)
	for i, err := range a.SelfCheck() {
		if err != nil {
			t.Errorf("invariant %d: %v", i, err)
		}
	}
}

// Concurrent adds/removes on own replicas plus random merges on random pairs;
// after SyncAll every replica must equal the deterministic expectation.
func TestConcurrent(t *testing.T) {
	const n, ops = 4, 60
	a, _ := New(n, n*ops+1)
	var wg sync.WaitGroup
	for g := 0; g < n; g++ {
		wg.Add(2)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < ops; i++ {
				e := fmt.Sprintf("g%d-%d", g, i)
				if err := a.Add(g, e); err != nil {
					t.Error(err)
				}
				if i%3 == 2 {
					if err := a.Remove(g, e); err != nil {
						t.Error(err)
					}
				}
			}
		}(g)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(g)))
			for i := 0; i < ops; i++ {
				if err := a.Merge(rng.Intn(n), rng.Intn(n)); err != nil {
					t.Error(err)
				}
			}
		}(g)
	}
	wg.Wait()
	if err := a.SyncAll(); err != nil {
		t.Fatal(err)
	}
	want := n * (ops - ops/3)
	for r := 0; r < n; r++ {
		el, _ := a.Elements(r)
		if len(el) != want {
			t.Fatalf("replica %d: %d elements, want %d", r, len(el), want)
		}
	}
}

func TestFaultInjection(t *testing.T) {
	a, _ := New(2, 1)
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(a.Add(0, "e"))
	must(a.Add(1, "f"))
	newN := func() error { _, err := New(0, 1); return err }
	newM := func() error { _, err := New(1, 0); return err }
	cases := []struct {
		name string
		err  error
		want error
	}{
		{"bad n", newN(), orset.ErrParam},
		{"bad maxTags", newM(), orset.ErrParam},
		{"add bad replica", a.Add(2, "x"), orset.ErrParam},
		{"remove bad replica", a.Remove(-1, "x"), orset.ErrParam},
		{"merge bad dst", a.Merge(2, 0), orset.ErrParam},
		{"merge bad src", a.Merge(0, -1), orset.ErrParam},
		{"empty element", a.Add(0, ""), orset.ErrEmpty},
		{"missing remove", a.Remove(0, "zz"), orset.ErrNotFound},
		{"overflow add", a.Add(0, "g"), orset.ErrCapacity},
		{"overflow merge", a.Merge(0, 1), orset.ErrCapacity},
	}
	for _, c := range cases {
		if !errors.Is(c.err, c.want) {
			t.Errorf("%s: %v, want %v", c.name, c.err, c.want)
		}
	}
	kinds := []error{orset.ErrParam, orset.ErrEmpty, orset.ErrNotFound, orset.ErrCapacity}
	for i, x := range kinds {
		for _, y := range kinds[i+1:] {
			if errors.Is(x, y) || errors.Is(y, x) {
				t.Fatal("error kinds not distinct")
			}
		}
	}
	el, _ := a.Elements(0)
	if len(el) != 1 || len(el["e"]) != 1 || el["e"][0].S != 1 {
		t.Fatal("rejected ops changed replica 0")
	}
	el, _ = a.Elements(1)
	if len(el) != 1 || len(el["f"]) != 1 || el["f"][0].S != 1 {
		t.Fatal("rejected ops changed replica 1")
	}
	must(a.Remove(0, "e")) // still usable after rejections
}
