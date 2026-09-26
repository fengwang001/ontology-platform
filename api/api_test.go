package api_test

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"ontology/api"
	"ontology/ch"
)

// ch is covered here (it has no test file of its own): the fixed row
// coefficients behind the NOTES.md worked example, range, and its sentinel.
func TestCHFamily(t *testing.T) {
	f, err := ch.NewFamily(6)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		j   int
		x   int64
		col int
	}{
		{1, 2, 2}, {2, 2, 5}, {3, 2, 1}, // Add(2) -> (2,5,1)
		{1, 5, 5}, {2, 5, 5}, {3, 5, 4}, // Add(5) -> (5,5,4)
		{1, 11, 5}, {2, 11, 5}, {3, 11, 4}, // Add(11) -> (5,5,4)
		{4, 0, 4}, // j>3: a=7,b=4
	}
	for _, c := range want {
		if g := f.Column(c.j, c.x); g != c.col {
			t.Errorf("h_%d(%d)=%d want %d", c.j, c.x, g, c.col)
		}
	}
	if _, err := ch.NewFamily(0); !errors.Is(err, ch.ErrBadWidth) {
		t.Fatalf("NewFamily(0) err=%v", err)
	}
	if a, b := ch.Coeff(4); a != 7 || b != 4 {
		t.Fatalf("Coeff(4)=(%d,%d) want (7,4)", a, b)
	}
}

func TestRejectedOpsLeaveState(t *testing.T) {
	if _, err := api.New(0, 3); !errors.Is(err, api.ErrInvalidParams) {
		t.Fatalf("New(0,3) err=%v", err)
	}
	a, err := api.New(6, 3)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Add(2, 4); err != nil {
		t.Fatal(err)
	}
	if errors.Is(api.ErrInvalidParams, api.ErrInvalidCount) ||
		errors.Is(api.ErrInvalidCount, api.ErrInvalidKey) ||
		errors.Is(api.ErrInvalidParams, api.ErrInvalidKey) {
		t.Fatal("the three API sentinels must be mutually distinct")
	}
	probe := func() [3]int64 {
		var p [3]int64
		for i, k := range []int64{2, 5, 9} {
			v, err := a.Query(k)
			if err != nil {
				t.Fatal(err)
			}
			p[i] = v
		}
		return p
	}
	bad := []struct {
		k, c int64
		want error
	}{
		{-1, 1, api.ErrInvalidKey}, {1, 0, api.ErrInvalidCount}, {1, -7, api.ErrInvalidCount},
	}
	before := probe()
	for _, b := range bad {
		if err := a.Add(b.k, b.c); !errors.Is(err, b.want) {
			t.Fatalf("Add(%d,%d) err=%v want %v", b.k, b.c, err, b.want)
		}
		if _, err := a.Query(-3); !errors.Is(err, api.ErrInvalidKey) {
			t.Fatalf("Query(-3) err=%v", err)
		}
	}
	if after := probe(); after != before {
		t.Fatalf("rejected calls changed state: %v -> %v", before, after)
	}
	if err := a.Add(5, 2); err != nil { // still usable afterwards
		t.Fatal(err)
	}
	if v, _ := a.Query(5); v != 2 {
		t.Fatalf("Query(5)=%d want 2 after rejections", v)
	}
	if err := a.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}
func TestConcurrentQueries(t *testing.T) {
	a, err := api.New(256, 5)
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(99))
	const seen = 500
	keys := make([]int64, seen)
	for i := range keys {
		keys[i] = rng.Int63n(100000)
		if err := a.Add(keys[i], rng.Int63n(9)+1); err != nil {
			t.Fatal(err)
		}
	}
	reference := make([]int64, seen)
	for i, k := range keys {
		reference[i], _ = a.Query(k)
	}
	const N = 16
	var wg sync.WaitGroup
	errs := make(chan error, 2*N)
	run := func(fn func() error) {
		defer wg.Done()
		if err := fn(); err != nil {
			errs <- err
		}
	}
	for g := 0; g < N; g++ { // every goroutine must see key-wise identical results
		wg.Add(1)
		go run(func() error {
			for i, k := range keys {
				v, err := a.Query(k)
				if err != nil {
					return err
				}
				if v != reference[i] {
					return fmt.Errorf("Query(%d)=%d, want %d", k, v, reference[i])
				}
			}
			return nil
		})
	}
	for g := 0; g < 4; g++ { // SelfCheck runs concurrently with the Queries
		wg.Add(1)
		go run(a.SelfCheck)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
