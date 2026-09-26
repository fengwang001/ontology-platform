package api_test

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"ontology/api"
	"ontology/vv"
)

// TestSelfCheck replays the built-in sequence covering all four invariants.
func TestSelfCheck(t *testing.T) {
	if err := api.New().SelfCheck(); err != nil {
		t.Fatalf("SelfCheck failed: %v", err)
	}
}

// TestSentinelErrorsDistinct pins invariant 4: every failure mode has a
// decidable error and the three errors are pairwise distinct.
func TestSentinelErrorsDistinct(t *testing.T) {
	a := api.New()
	cases := []struct {
		name string
		call func() error
		want error
	}{
		{"negative counter", func() error { return a.Set("x", vv.Vector{0: -1}) }, api.ErrNegativeCounter},
		{"negative actor", func() error { return a.Set("y", vv.Vector{-1: 1}) }, api.ErrNegativeActor},
		{"unknown name merge", func() error { _, e := a.Merge("nope", "x"); return e }, api.ErrUnknownName},
		{"unknown name compare", func() error { _, e := a.Compare("x", "nope"); return e }, api.ErrUnknownName},
	}
	for _, c := range cases {
		if err := c.call(); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, err, c.want)
		}
	}
	errs := []error{api.ErrNegativeCounter, api.ErrNegativeActor, api.ErrUnknownName}
	for i := 0; i < len(errs); i++ {
		for j := i + 1; j < len(errs); j++ {
			if errs[i] == errs[j] {
				t.Errorf("sentinel errors %d and %d are identical", i, j)
			}
		}
	}
}

// TestRejectedOpsLeaveNoTrace pins invariant 4: rejected calls mutate
// nothing, and the registry stays fully usable afterwards.
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	a := api.New()
	if err := a.Set("r1", vv.Vector{0: 1}); err != nil {
		t.Fatal(err)
	}
	if err := a.Set("r2", vv.Vector{0: 1, 1: 2}); err != nil {
		t.Fatal(err)
	}
	rejected := []struct {
		name string
		call func() error
		want error
	}{
		{"set negative counter", func() error { return a.Set("r1", vv.Vector{0: -1}) }, api.ErrNegativeCounter},
		{"set negative actor", func() error { return a.Set("r3", vv.Vector{-1: 1}) }, api.ErrNegativeActor},
		{"merge unknown name", func() error { _, e := a.Merge("r1", "r3"); return e }, api.ErrUnknownName},
		{"compare unknown name", func() error { _, e := a.Compare("r3", "r1"); return e }, api.ErrUnknownName},
	}
	for _, c := range rejected {
		if err := c.call(); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, err, c.want)
		}
	}
	m, err := a.Merge("r1", "r2")
	if err != nil || !reflect.DeepEqual(m, vv.Vector{0: 1, 1: 2}) {
		t.Fatalf("state changed: merge = %v, %v", m, err)
	}
	if c, _ := a.Compare("r1", "r2"); c != vv.Less {
		t.Fatalf("state changed: compare = %s", c)
	}
	if _, err := a.Merge("r3", "r3"); !errors.Is(err, api.ErrUnknownName) {
		t.Fatal("rejected Set created the name it was supposed to reject")
	}
	if err := a.Set("r3", vv.Vector{2: 1}); err != nil { // still usable
		t.Fatalf("registry unusable after rejections: %v", err)
	}
	if c, _ := a.Compare("r1", "r3"); c != vv.Concurrent {
		t.Fatalf("post-rejection Set broken: compare = %s", c)
	}
}

// TestConcurrentMergeConsistent: N goroutines merge the same registered
// pair concurrently; every result must be key-for-key identical.
func TestConcurrentMergeConsistent(t *testing.T) {
	a := api.New()
	if err := a.Set("r2", vv.Vector{0: 1, 1: 2}); err != nil {
		t.Fatal(err)
	}
	if err := a.Set("r3", vv.Vector{1: 1, 2: 1}); err != nil {
		t.Fatal(err)
	}
	const n = 100
	results := make([]vv.Vector, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			var err error
			results[i], err = a.Merge("r2", "r3")
			if err != nil {
				t.Errorf("goroutine %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	want := vv.Vector{0: 1, 1: 2, 2: 1}
	for i, got := range results {
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("goroutine %d got %v, want %v", i, got, want)
		}
	}
}
