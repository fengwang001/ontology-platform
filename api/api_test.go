package api

import (
	"errors"
	"sync"
	"testing"
)

func TestSelfCheck(t *testing.T) {
	a, err := New(8)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

// TestSentinelsDistinct: the four failure kinds must be pairwise distinct.
func TestSentinelsDistinct(t *testing.T) {
	s := []error{ErrBadConfig, ErrTooMany, ErrAbsent, ErrEmpty}
	for i := range s {
		for j := i + 1; j < len(s); j++ {
			if s[i] == s[j] {
				t.Fatalf("sentinels %d and %d are the same", i, j)
			}
		}
	}
}

// TestRejectedOpsLeaveState pins invariant 4: every rejected op is the right
// sentinel, leaves Count untouched, and the instance stays usable.
func TestRejectedOpsLeaveState(t *testing.T) {
	cases := []struct {
		name string
		cap  int
		seed []int64
		want error
		call func(a *API) error
	}{
		{"empty-median", 4, nil, ErrEmpty, func(a *API) error { _, e := a.Median(); return e }},
		{"empty-p90", 4, nil, ErrEmpty, func(a *API) error { _, e := a.QuantileP90(); return e }},
		{"too-many", 1, []int64{1}, ErrTooMany, func(a *API) error { return a.Insert(2) }},
		{"absent-delete", 4, []int64{1, 2}, ErrAbsent, func(a *API) error { return a.Delete(9) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, _ := New(c.cap)
			for _, v := range c.seed {
				if err := a.Insert(v); err != nil {
					t.Fatal(err)
				}
			}
			if err := c.call(a); !errors.Is(err, c.want) {
				t.Fatalf("got %v, want %v", err, c.want)
			}
			if a.Count() != len(c.seed) {
				t.Fatalf("rejected op changed state: count=%d want %d", a.Count(), len(c.seed))
			}
			if len(c.seed) > 0 {
				if _, err := a.Median(); err != nil {
					t.Fatalf("instance broken after rejection: %v", err)
				}
			}
		})
	}
	if _, err := New(0); !errors.Is(err, ErrBadConfig) {
		t.Fatalf("New(0) err=%v want ErrBadConfig", err)
	}
	// A full instance that rejected inserts must recover once space frees up.
	a, _ := New(1)
	if err := a.Insert(1); err != nil {
		t.Fatal(err)
	}
	if err := a.Insert(2); !errors.Is(err, ErrTooMany) {
		t.Fatalf("expected ErrTooMany, got %v", err)
	}
	if err := a.Delete(1); err != nil {
		t.Fatal(err)
	}
	if err := a.Insert(3); err != nil || a.Count() != 1 {
		t.Fatalf("instance not usable after rejection: err=%v count=%d", err, a.Count())
	}
}

// TestConcurrentReadOnly: many goroutines read a filled instance with no
// sleeps; every median and p90 must be field-for-field identical. -race clean.
func TestConcurrentReadOnly(t *testing.T) {
	const total, readers = 5000, 64
	a, _ := New(total)
	for i := 0; i < total; i++ {
		if err := a.Insert(int64(i*7 - total)); err != nil {
			t.Fatal(err)
		}
	}
	wantM, mErr := a.Median()
	wantP, pErr := a.QuantileP90()
	if mErr != nil || pErr != nil {
		t.Fatal("seed quantile error")
	}
	type res struct {
		m float64
		p int64
		c int
	}
	results := make([]res, readers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for g := 0; g < readers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-start // barrier: readers launch together, no sleep anywhere
			for j := 0; j < 50; j++ {
				m, e1 := a.Median()
				p, e2 := a.QuantileP90()
				if e1 != nil || e2 != nil {
					t.Errorf("concurrent read error: %v %v", e1, e2)
					return
				}
				results[g] = res{m, p, a.Count()}
			}
		}(g)
	}
	close(start)
	wg.Wait()
	for g := 0; g < readers; g++ {
		if results[g].m != wantM || results[g].p != wantP || results[g].c != total {
			t.Fatalf("reader %d got %+v, want {%v %d %d}", g, results[g], wantM, wantP, total)
		}
	}
}
