package api_test

import (
	"errors"
	"math"
	"math/rand"
	"sync"
	"testing"

	"ontology/api"
)

func naive(a []int64, l, r int) int64 {
	m := int64(math.MaxInt64)
	for _, v := range a[l:r] {
		m = min(m, v)
	}
	return m
}

// sweep compares every interval [l,r) on r against a naive scan of arr.
func sweep(t *testing.T, r *api.RMQ, arr []int64) {
	for l := 0; l <= len(arr); l++ {
		for rr := l; rr <= len(arr); rr++ {
			got, err := r.Query(l, rr)
			if err != nil || got != naive(arr, l, rr) {
				t.Fatalf("Query(%d,%d)=(%d,%v), want %d", l, rr, got, err, naive(arr, l, rr))
			}
		}
	}
}

// TestNaiveAgreement pins invariant 1: every [l,r) equals a naive scan.
func TestNaiveAgreement(t *testing.T) {
	for _, arr := range [][]int64{{42}, {5, 2, 8, 1, 9, 3, 7, 4}, {-7, -7, 0, 1 << 40, -(1 << 40)}} {
		r, err := api.New(arr)
		if err != nil {
			t.Fatal(err)
		}
		sweep(t, r, arr)
	}
}

// TestUpdatePropagation pins invariant 2: no stale ancestor after updates.
func TestUpdatePropagation(t *testing.T) {
	arr := []int64{5, 2, 8, 1, 9, 3, 7, 4}
	r, _ := api.New(arr)
	rng := rand.New(rand.NewSource(611))
	for step := 0; step < 32; step++ {
		i := rng.Intn(len(arr))
		arr[i] = rng.Int63n(2000) - 1000
		if err := r.Update(i, arr[i]); err != nil {
			t.Fatal(err)
		}
		sweep(t, r, arr)
	}
}

// TestPaddingIdentity pins invariant 3: +Inf padding never pollutes [0,n).
func TestPaddingIdentity(t *testing.T) {
	for n := 1; n <= 40; n++ {
		arr := make([]int64, n)
		for i := range arr {
			arr[i] = int64((i*7 + 3) % 13)
		}
		r, err := api.New(arr)
		if err != nil {
			t.Fatal(err)
		}
		sweep(t, r, arr)
	}
}

// TestRejectedOpsNoStateChange pins invariant 4: the four distinct
// failures all fire and the complete query table is identical afterwards.
func TestRejectedOpsNoStateChange(t *testing.T) {
	arr := []int64{5, 2, 8, 1, 9, 3}
	r, _ := api.New(arr)
	bad := []struct {
		want error
		fn   func() error
	}{
		{api.ErrInvalidRange, func() error { _, e := r.Query(2, 1); return e }},
		{api.ErrRangeOutOfBounds, func() error { _, e := r.Query(-1, 0); return e }},
		{api.ErrRangeOutOfBounds, func() error { _, e := r.Query(0, 7); return e }},
		{api.ErrIndexOutOfBounds, func() error { return r.Update(6, 0) }},
		{api.ErrEmptyInput, func() error { _, e := api.New(nil); return e }},
	}
	for _, c := range bad {
		if got := c.fn(); !errors.Is(got, c.want) {
			t.Errorf("got %v, want %v", got, c.want)
		}
	}
	sweep(t, r, arr)
}

// TestEightSteps reproduces the mandatory NOTES.md eight-row table; kind
// 1 is an update (i,v), kind 0 a query [a,b) whose answer is recorded.
func TestEightSteps(t *testing.T) {
	r, _ := api.New([]int64{5, 2, 8, 1, 9, 3, 7, 4})
	var got []int64
	for _, s := range [][3]int{{0, 0, 8}, {1, 0, 10}, {0, 0, 4}, {1, 3, 11},
		{0, 0, 4}, {0, 4, 8}, {1, 2, 0}, {0, 0, 8}} {
		if s[0] == 1 {
			r.Update(s[1], int64(s[2]))
		} else {
			v, _ := r.Query(s[1], s[2])
			got = append(got, v)
		}
	}
	want := []int64{1, 1, 2, 3, 0}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("query step %d: got %d, want %d", i+1, got[i], want[i])
		}
	}
}
func TestSelfCheck(t *testing.T) {
	r, _ := api.New([]int64{5, 2, 8, 1, 9, 3})
	if err := r.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

// TestConcurrentReaders: 64 goroutines run the same reads; WaitGroup only.
func TestConcurrentReaders(t *testing.T) {
	arr := []int64{7, 2, 5, 1, 8, 3, 9, 4, 0, 6, 2}
	r, _ := api.New(arr)
	qs := [][2]int{{0, 11}, {2, 7}, {0, 1}, {5, 5}, {3, 10}, {1, 4}}
	var wg sync.WaitGroup
	for g := 0; g < 64; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, qd := range qs {
				v, err := r.Query(qd[0], qd[1])
				if err != nil || v != naive(arr, qd[0], qd[1]) {
					t.Errorf("Query(%d,%d)=(%d,%v)", qd[0], qd[1], v, err)
				}
			}
			if r.Size() != len(arr) {
				t.Errorf("Size=%d", r.Size())
			}
			if err := r.SelfCheck(); err != nil {
				t.Errorf("SelfCheck: %v", err)
			}
		}()
	}
	wg.Wait()
}
