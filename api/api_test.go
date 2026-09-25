package api_test

import (
	"bytes"
	"errors"
	"math/bits"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/api"
	"ontology/query"
	"ontology/rhash"
)

func randBytes(rng *rand.Rand, n int) []byte {
	s := make([]byte, n)
	for i := range s {
		s[i] = byte(rng.Intn(4)) // small alphabet: frequent true equalities
	}
	return s
}

func naiveLCP(s []byte, i, j int) int {
	k := 0
	for i+k < len(s) && j+k < len(s) && s[i+k] == s[j+k] {
		k++
	}
	return k
}

// Must stay first in this file: it checks ErrNotBuilt before any New.
func TestErrorsDistinct(t *testing.T) {
	_, e1 := api.Equal(0, 1, 0, 1)
	_, e2 := api.LCP(0, 0)
	if !errors.Is(e1, api.ErrNotBuilt) || !errors.Is(e2, api.ErrNotBuilt) ||
		!errors.Is(api.New(nil), api.ErrEmpty) ||
		api.ErrEmpty == api.ErrRange || api.ErrRange == api.ErrNotBuilt {
		t.Fatal("sentinels wrong: need distinct ErrEmpty/ErrRange/ErrNotBuilt")
	}
}

// Invariant 1: Equal must agree with bytes.Equal on every sampled range.
func TestEqualMatchesNaive(t *testing.T) {
	for _, n := range []int{1, 2, 100, 1000, 10000} {
		rng := rand.New(rand.NewSource(int64(n)))
		s := randBytes(rng, n)
		api.New(s)
		for k := 0; k < 300; k++ {
			l1, r1 := rng.Intn(n+1), 0
			r1 = l1 + rng.Intn(n-l1+1)
			l2, r2 := rng.Intn(n+1), 0
			r2 = l2 + rng.Intn(n-l2+1)
			got, err := api.Equal(l1, r1, l2, r2)
			if err != nil || got != bytes.Equal(s[l1:r1], s[l2:r2]) {
				t.Fatalf("n=%d Equal(%d,%d,%d,%d)=%v,%v", n, l1, r1, l2, r2, got, err)
			}
		}
	}
}

// Invariant 3: LCP agrees with byte-by-byte comparison; each LCP uses
// at most ceil(log2 n)+1 hash comparisons.
func TestLCPMatchesNaive(t *testing.T) {
	for _, n := range []int{1, 2, 100, 1000, 10000} {
		rng := rand.New(rand.NewSource(int64(n)))
		s := randBytes(rng, n)
		api.New(s)
		q := query.New(rhash.New(s))
		bound := bits.Len(uint(n-1)) + 1
		for k := 0; k < 300; k++ {
			i, j := rng.Intn(n+1), rng.Intn(n+1)
			got, err := api.LCP(i, j)
			if err != nil || got != naiveLCP(s, i, j) {
				t.Fatalf("n=%d LCP(%d,%d)=%d,%v want %d", n, i, j, got, err, naiveLCP(s, i, j))
			}
			if _, cmp := q.LCP(i, j); cmp > bound {
				t.Fatalf("n=%d: %d comparisons > bound %d", n, cmp, bound)
			}
		}
	}
}

// Invariant 4: rejected calls signal distinct errors and change nothing.
func TestRejectionLeavesState(t *testing.T) {
	api.New([]byte("abracadabra"))
	before, _ := api.Equal(0, 3, 7, 10)
	beforeL, _ := api.LCP(0, 7)
	bad := [][]int{{-1, 1, 0, 1}, {0, 12, 0, 1}, {3, 1, 0, 1}, {0, 1, 2, 12}}
	for _, b := range bad {
		if _, err := api.Equal(b[0], b[1], b[2], b[3]); !errors.Is(err, api.ErrRange) {
			t.Fatalf("Equal%v: got %v, want ErrRange", b, err)
		}
	}
	if _, err := api.LCP(-1, 0); !errors.Is(err, api.ErrRange) {
		t.Fatal("LCP(-1,0): want ErrRange")
	}
	if !errors.Is(api.New(nil), api.ErrEmpty) {
		t.Fatal("New(nil) must keep failing with ErrEmpty")
	}
	after, _ := api.Equal(0, 3, 7, 10)
	afterL, _ := api.LCP(0, 7)
	if after != before || afterL != beforeL {
		t.Fatal("state changed after rejected operations")
	}
	if err := api.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

// Concurrent Equal/LCP must be race-clean and agree item by item.
func TestConcurrent(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	s := randBytes(rng, 5000)
	api.New(s)
	qs := make([][6]int, 200)
	for k := range qs {
		l1, l2 := rng.Intn(len(s)+1), rng.Intn(len(s)+1)
		qs[k] = [6]int{l1, l1 + rng.Intn(len(s)-l1+1), l2, l2 + rng.Intn(len(s)-l2+1),
			rng.Intn(len(s) + 1), rng.Intn(len(s) + 1)}
	}
	var bad int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for _, q := range qs {
				eq, e1 := api.Equal(q[0], q[1], q[2], q[3])
				l, e2 := api.LCP(q[4], q[5])
				if e1 != nil || e2 != nil ||
					eq != bytes.Equal(s[q[0]:q[1]], s[q[2]:q[3]]) ||
					l != naiveLCP(s, q[4], q[5]) {
					atomic.StoreInt32(&bad, 1)
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	if bad != 0 {
		t.Fatal("concurrent goroutines disagree with the naive reference")
	}
}
