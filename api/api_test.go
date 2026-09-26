package api

import (
	"errors"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
)

func mustNew(t *testing.T, w, d int) *API {
	a, err := New(w, d)
	if err != nil {
		t.Fatalf("New(%d,%d): %v", w, d, err)
	}
	return a
}
func mustAdd(t *testing.T, a *API, k, c int64) {
	if err := a.Add(k, c); err != nil {
		t.Fatalf("Add(%d,%d): %v", k, c, err)
	}
}

// Invariant 1: Query never below the true frequency.
func TestNeverUnderestimate(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for _, m := range []int{100, 1000, 10000} {
		a, ref := mustNew(t, 8, 3), map[int64]int64{}
		for i := 0; i < m; i++ {
			k, c := rng.Int63n(40), 1+rng.Int63n(9)
			mustAdd(t, a, k, c)
			ref[k] += c
		}
		for k, c := range ref {
			if q, _ := a.Query(k); q < c {
				t.Fatalf("m=%d key=%d: got %d below true %d", m, k, q, c)
			}
		}
	}
}

// Invariant 2: a lone key is reported exactly.
func TestSingleKeyExact(t *testing.T) {
	for _, c := range []int64{1, 5, 1000} {
		a := mustNew(t, 6, 3)
		mustAdd(t, a, 7, c)
		if q, _ := a.Query(7); q != c {
			t.Fatalf("count=%d: got %d", c, q)
		}
	}
}

// Invariant 3: matches an exact map — equality when collision-free,
// >= when collisions are forced.
func TestExactMapReference(t *testing.T) {
	a := mustNew(t, 9, 3) // w=9 coprime to 2 and 5: keys 0..8 collide nowhere
	for k := int64(0); k < 9; k++ {
		mustAdd(t, a, k, k+1)
		if q, _ := a.Query(k); q != k+1 { // collision-free: later adds can't change this
			t.Fatalf("collision-free key %d: got %d want %d", k, q, k+1)
		}
	}
	b, ref := mustNew(t, 2, 3), map[int64]int64{} // tiny width forces collisions
	for _, e := range [][2]int64{{1, 3}, {2, 5}, {3, 7}, {4, 2}, {5, 1}} {
		mustAdd(t, b, e[0], e[1])
		ref[e[0]] += e[1]
	}
	for k, c := range ref {
		if q, _ := b.Query(k); q < c {
			t.Fatalf("colliding key %d: got %d below true %d", k, q, c)
		}
	}
}

// Section 5: the three rejections are distinct sentinels.
func TestErrorsDistinct(t *testing.T) {
	a := mustNew(t, 6, 3)
	_, eDim := New(0, 3)
	_, eDim2 := New(3, -1)
	eKey, eCnt := a.Add(-1, 1), a.Add(1, 0)
	_, eQK := a.Query(-1)
	for _, tc := range [][2]error{{eDim, ErrBadDim}, {eDim2, ErrBadDim}, {eKey, ErrBadKey}, {eCnt, ErrBadCount}, {eQK, ErrBadKey}} {
		if !errors.Is(tc[0], tc[1]) {
			t.Fatalf("got %v want %v", tc[0], tc[1])
		}
	}
	if eDim == eKey || eDim == eCnt || eKey == eCnt {
		t.Fatal("sentinel errors not distinct")
	}
}

// Invariant 4: rejected ops change nothing and the sketch stays usable.
func TestRejectionLeavesStateUntouched(t *testing.T) {
	a := mustNew(t, 6, 3)
	mustAdd(t, a, 2, 4)
	b2, _ := a.Query(2)
	b5, _ := a.Query(5)
	_ = a.Add(-1, 1)
	_ = a.Add(2, 0)
	_ = a.Add(2, -3)
	a2, _ := a.Query(2)
	a5, _ := a.Query(5)
	if a2 != b2 || a5 != b5 {
		t.Fatalf("state changed: %d->%d, %d->%d", b2, a2, b5, a5)
	}
	mustAdd(t, a, 2, 1)
	if q, _ := a.Query(2); q != 5 {
		t.Fatalf("sketch unusable after rejections: got %d want 5", q)
	}
}

// Section 6: concurrent queries agree key by key, no sleeps.
func TestConcurrentQuery(t *testing.T) {
	a := mustNew(t, 16, 4)
	for k := 0; k < 64; k++ {
		mustAdd(t, a, int64(k), int64(k)+1)
	}
	want := make([]int64, 64)
	for k := range want {
		want[k], _ = a.Query(int64(k))
	}
	var wg sync.WaitGroup
	var bad atomic.Int64
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k, w := range want {
				if q, _ := a.Query(int64(k)); q != w {
					bad.Add(1)
				}
			}
		}()
	}
	for g := 0; g < 4; g++ { // SelfCheck is race-clean alongside the queries
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := SelfCheck(); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if bad.Load() > 0 {
		t.Fatalf("%d mismatches under concurrency", bad.Load())
	}
}
