package api_test

import (
	"errors"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/api"
)

type buildCase struct {
	keys []int
	m, p int
}

func mustBuild(t *testing.T, keys []int, m, p int) *api.Structure {
	t.Helper()
	s := api.New()
	if err := s.Build(keys, m, p); err != nil {
		t.Fatalf("Build(%v,%d,%d): %v", keys, m, p, err)
	}
	return s
}

// TestSixKeyBuckets pins the NOTES six-key set: all six hit; Lookup(7), which maps to bucket 1 slot 3 holding 19, is rejected as ErrNotFound.
func TestSixKeyBuckets(t *testing.T) {
	keys := []int{5, 11, 13, 17, 19, 24}
	s := mustBuild(t, keys, 6, 29)
	for _, k := range keys {
		if ok, _ := s.Lookup(k); !ok {
			t.Fatalf("built key %d not found", k)
		}
	}
	if ok, err := s.Lookup(7); ok || !errors.Is(err, api.ErrNotFound) {
		t.Fatalf("Lookup(7)=(%v,%v), want (false,ErrNotFound)", ok, err)
	}
}

// TestBuildLookup round-trips builds: every built key is found (invariant I1).
func TestBuildLookup(t *testing.T) {
	for _, c := range []buildCase{{[]int{0}, 1, 2}, {[]int{3, 8}, 2, 11}, {[]int{1, 4, 9, 16, 25}, 5, 29}} {
		s := mustBuild(t, c.keys, c.m, c.p)
		for _, x := range c.keys {
			if ok, err := s.Lookup(x); !ok || err != nil {
				t.Fatalf("Lookup(%d)=(%v,%v), want hit", x, ok, err)
			}
		}
	}
}

// TestReferenceConsistency loops random sets at several m scales: Lookup over the full residue range equals a naive map[int]bool (invariant I2).
func TestReferenceConsistency(t *testing.T) {
	for _, c := range []buildCase{{nil, 1, 5}, {nil, 7, 23}, {nil, 13, 41}, {nil, 31, 97}, {nil, 64, 193}} {
		n := 1 + rand.Intn(2*c.m)
		keys := rand.Perm(c.p - 1)[:n] // distinct values 0..p-2, all < p
		s := mustBuild(t, keys, c.m, c.p)
		ref := map[int]bool{}
		for _, k := range keys {
			ref[k] = true
		}
		for x := 0; x < c.p; x++ {
			ok, err := s.Lookup(x)
			if ok != ref[x] || (err == nil) != ref[x] {
				t.Fatalf("m=%d Lookup(%d)=(%v,%v), ref=%v", c.m, x, ok, err, ref[x])
			}
		}
	}
}

// TestSentinelErrors verifies the four pairwise-distinct rejection causes.
func TestSentinelErrors(t *testing.T) {
	s := api.New()
	for _, c := range []struct {
		bc   buildCase
		want error
	}{
		{buildCase{[]int{1, 2, 2}, 4, 5}, api.ErrDuplicateKey},
		{buildCase{nil, 4, 5}, api.ErrEmptyKeys},
		{buildCase{[]int{1, 2}, 0, 5}, api.ErrInvalidParam},
		{buildCase{[]int{1, 2}, 4, 6}, api.ErrInvalidParam},
		{buildCase{[]int{1, 7}, 4, 7}, api.ErrInvalidParam},
	} {
		if err := s.Build(c.bc.keys, c.bc.m, c.bc.p); !errors.Is(err, c.want) {
			t.Errorf("Build %v: got %v, want %v", c.bc.keys, err, c.want)
		}
	}
	if api.ErrDuplicateKey == api.ErrEmptyKeys || api.ErrEmptyKeys == api.ErrNotFound || api.ErrNotFound == api.ErrInvalidParam || api.ErrInvalidParam == api.ErrDuplicateKey {
		t.Fatal("sentinel errors must be pairwise distinct")
	}
	if ok, err := s.Lookup(99); ok || !errors.Is(err, api.ErrNotFound) {
		t.Fatalf("Lookup before any build: (%v,%v)", ok, err)
	}
}

// TestRejectedState proves failed Builds never alter a working structure (I4).
func TestRejectedState(t *testing.T) {
	keys := []int{3, 8, 15, 22}
	s := mustBuild(t, keys, 5, 23)
	for i, b := range []buildCase{{[]int{1, 1}, 5, 29}, {nil, 5, 29}, {[]int{1, 2}, 0, 29}, {[]int{1, 2}, 5, 22}} {
		if s.Build(b.keys, b.m, b.p) == nil {
			t.Fatalf("case %d: rejection expected", i)
		}
		if s.Size() != 4 {
			t.Fatalf("case %d: Size changed to %d", i, s.Size())
		}
		if ok, _ := s.Lookup(3); !ok {
			t.Fatalf("case %d: key 3 lost after rejection", i)
		}
	}
}

// TestConcurrentLookup: many goroutines do read-only Lookups; results must agree with the naive reference. No sleeps (I2 verified under -race).
func TestConcurrentLookup(t *testing.T) {
	keys := []int{2, 5, 11, 17, 29, 42, 70, 100}
	s := mustBuild(t, keys, 16, 101)
	ref := map[int]bool{}
	for _, k := range keys {
		ref[k] = true
	}
	const N, rounds = 64, 300
	var wg sync.WaitGroup
	var bad atomic.Bool
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				x := (seed*7 + i*13) % 40
				ok, err := s.Lookup(x)
				if ok != ref[x] || (err == nil) != ref[x] || s.Size() != len(keys) {
					bad.Store(true)
					return
				}
			}
		}(g + 1)
	}
	wg.Wait()
	if bad.Load() {
		t.Fatal("concurrent read diverged from reference")
	}
}

// TestSelfCheck exercises the public self-check on the built-in key set.
func TestSelfCheck(t *testing.T) {
	if err := api.New().SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}
