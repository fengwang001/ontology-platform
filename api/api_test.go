package api_test

import (
	"errors"
	"math/rand"
	"sync"
	"testing"

	"ontology/api"
)

func nextPrime(n int) int {
	for p := n + 1; ; p++ {
		d := 2
		for ; d*d <= p && p%d != 0; d++ {
		}
		if d*d > p {
			return p
		}
	}
}

func randKeys(rng *rand.Rand, n, bound int) []int {
	set := make(map[int]struct{}, n)
	for len(set) < n {
		set[rng.Intn(bound)] = struct{}{}
	}
	out := make([]int, 0, n)
	for k := range set {
		out = append(out, k)
	}
	return out
}

// Lookup must agree with a naive map[int]bool reference, key by key.
func TestNaiveReference(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for _, c := range []struct{ n, m int }{{1, 1}, {6, 7}, {50, 53}, {200, 211}, {1000, 1009}} {
		keys := randKeys(rng, c.n, 20*c.n)
		tbl, err := api.Build(keys, c.m, nextPrime(20*c.n))
		if err != nil {
			t.Fatalf("n=%d m=%d: %v", c.n, c.m, err)
		}
		naive := make(map[int]bool, c.n)
		for _, k := range keys {
			naive[k] = true
		}
		for x := 0; x <= 20*c.n; x++ {
			found, err := tbl.Lookup(x)
			if found != naive[x] || found != (err == nil) || (!found && !errors.Is(err, api.ErrNotFound)) {
				t.Fatalf("n=%d m=%d x=%d: found=%v err=%v naive=%v", c.n, c.m, x, found, err, naive[x])
			}
		}
	}
}

// The four rejection categories must be mutually distinguishable.
func TestErrorsDistinct(t *testing.T) {
	all := []error{api.ErrDuplicateKey, api.ErrEmptyKeys, api.ErrNotFound, api.ErrInvalidParam}
	for i := range all {
		for j := i + 1; j < len(all); j++ {
			if errors.Is(all[i], all[j]) {
				t.Fatalf("sentinels %d and %d indistinguishable", i, j)
			}
		}
	}
	cases := []struct {
		name string
		run  func() error
		want error
	}{
		{"duplicate", func() error { _, e := api.Build([]int{3, 3}, 4, 5); return e }, api.ErrDuplicateKey},
		{"empty", func() error { _, e := api.Build(nil, 4, 5); return e }, api.ErrEmptyKeys},
		{"m<1", func() error { _, e := api.Build([]int{3}, 0, 5); return e }, api.ErrInvalidParam},
		{"p not prime", func() error { _, e := api.Build([]int{3}, 4, 9); return e }, api.ErrInvalidParam},
		{"p<=max", func() error { _, e := api.Build([]int{3}, 4, 3); return e }, api.ErrInvalidParam},
		{"notfound", func() error { b, _ := api.Build([]int{3}, 4, 5); _, e := b.Lookup(4); return e }, api.ErrNotFound},
	}
	for _, c := range cases {
		if err := c.run(); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, err, c.want)
		}
	}
}

// Rejected operations must leave the built structure untouched.
func TestRejectionKeepsState(t *testing.T) {
	keys := []int{5, 11, 13, 17, 19, 24}
	tbl, err := api.Build(keys, 6, 29)
	if err != nil {
		t.Fatal(err)
	}
	rejects := []func() error{
		func() error { _, e := api.Build([]int{5, 5}, 6, 29); return e },
		func() error { _, e := api.Build(nil, 6, 29); return e },
		func() error { _, e := api.Build(keys, 0, 29); return e },
		func() error { _, e := api.Build(keys, 6, 28); return e },
	}
	for i, r := range rejects {
		if r() == nil {
			t.Fatalf("rejection %d unexpectedly succeeded", i)
		}
	}
	if _, err := tbl.Lookup(7); !errors.Is(err, api.ErrNotFound) {
		t.Fatalf("lookup absent: %v", err)
	}
	ok := tbl.Size() == len(keys)
	for _, k := range keys {
		found, _ := tbl.Lookup(k)
		ok = ok && found
	}
	if !ok {
		t.Fatal("state changed after rejections")
	}
}

// Concurrent read-only Lookup/Size/SelfCheck must agree with the naive map.
func TestConcurrentReads(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	keys := randKeys(rng, 500, 10000)
	tbl, err := api.Build(keys, 64, nextPrime(10000))
	if err != nil {
		t.Fatal(err)
	}
	naive := make(map[int]bool, len(keys))
	for _, k := range keys {
		naive[k] = true
	}
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			r := rand.New(rand.NewSource(seed))
			for i := 0; i < 2000; i++ {
				x := r.Intn(10001)
				found, err := tbl.Lookup(x)
				if found != naive[x] || found != (err == nil) || tbl.Size() != 500 {
					t.Errorf("x=%d found=%v err=%v naive=%v", x, found, err, naive[x])
					return
				}
			}
			if err := tbl.SelfCheck(); err != nil {
				t.Errorf("selfcheck: %v", err)
			}
		}(int64(g))
	}
	wg.Wait()
}
