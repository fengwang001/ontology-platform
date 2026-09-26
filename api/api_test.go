package api

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/lim"
)

// Test_ReferenceEquivalence (I1): random monotone sequences match a naive
// full-scan reference; same-t peers count (mutex-serialized capacity).
func Test_ReferenceEquivalence(t *testing.T) {
	cases := []struct{ limit, window, steps int64 }{
		{1, 5, 200}, {3, 10, 300}, {7, 4, 300}, {16, 50, 500},
	}
	for _, c := range cases {
		rng := rand.New(rand.NewSource(c.limit*1000 + c.window))
		r, _ := New(c.limit, c.window)
		var ref []int64 // naive accepted set
		var now int64
		for i := int64(0); i < c.steps; i++ {
			now += rng.Int63n(3) // non-decreasing; repeats allowed
			got, gerr := r.Allow(now)
			if gerr != nil {
				t.Fatalf("step %d: %v", i, gerr)
			}
			k := 0
			for _, ts := range ref {
				if ts >= now-c.window && ts <= now {
					k++
				}
			}
			if got != (int64(k) < c.limit) {
				t.Fatalf("l=%d w=%d step=%d t=%d: got %v k=%d", c.limit, c.window, i, now, got, k)
			}
			if got {
				ref = append(ref, now)
			}
			var live []int64 // queue retains only window-live accepted entries
			for _, ts := range ref {
				if ts >= now-c.window {
					live = append(live, ts)
				}
			}
			if set, _ := r.snapshot(); fmt.Sprint(set) != fmt.Sprint(live) {
				t.Fatalf("step %d: set %v != %v", i, set, live)
			}
		}
	}
}

// Test_ClockMonotonic (I3): rollback is rejected; lastT never decreases.
func Test_ClockMonotonic(t *testing.T) {
	cases := []struct {
		seq   []int64
		badAt int // index returning ErrClockRollback, -1 if all valid
	}{
		{[]int64{0, 1, 1, 2}, -1},
		{[]int64{10, 9}, 1},
		{[]int64{5, 5, 4}, 2},
		{[]int64{100, 100, 101}, -1},
	}
	for _, c := range cases {
		r, _ := New(2, 10)
		end := len(c.seq)
		if c.badAt >= 0 {
			end = c.badAt
		}
		for _, ts := range c.seq[:end] {
			if _, err := r.Allow(ts); err != nil {
				t.Fatalf("seq %v: %v", c.seq, err)
			}
		}
		if c.badAt >= 0 {
			if _, err := r.Allow(c.seq[c.badAt]); !errors.Is(err, lim.ErrClockRollback) {
				t.Fatalf("seq %v step %d: %v", c.seq, c.badAt, err)
			}
		}
		if _, lt := r.snapshot(); lt != c.seq[end-1] {
			t.Fatalf("lastT = %d, want %d", lt, c.seq[end-1])
		}
	}
}

// Test_FailureLeavesNoTrace (I4): distinct sentinels, no mutation on reject,
// stays usable afterwards, SelfCheck passes.
func Test_FailureLeavesNoTrace(t *testing.T) {
	for _, b := range [][2]int64{{0, 10}, {-1, 10}, {3, 0}, {3, -2}} {
		if _, err := New(b[0], b[1]); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("New(%d,%d): %v", b[0], b[1], err)
		}
	}
	if errors.Is(ErrInvalidConfig, lim.ErrNegativeTime) ||
		errors.Is(ErrInvalidConfig, lim.ErrClockRollback) ||
		errors.Is(lim.ErrNegativeTime, lim.ErrClockRollback) {
		t.Fatal("the three sentinel errors must be mutually distinct")
	}
	r, _ := New(3, 10)
	r.Allow(5)
	badT := []int64{-1, 4, -9, 3}
	badErr := []error{lim.ErrNegativeTime, lim.ErrClockRollback, lim.ErrNegativeTime, lim.ErrClockRollback}
	for i, ct := range badT {
		before, lastBefore := r.snapshot()
		if ok, err := r.Allow(ct); ok || !errors.Is(err, badErr[i]) {
			t.Fatalf("Allow(%d) = (%v,%v)", ct, ok, err)
		}
		if after, lastAfter := r.snapshot(); fmt.Sprint(after) != fmt.Sprint(before) || lastAfter != lastBefore {
			t.Fatalf("Allow(%d) left a trace: %v@%d -> %v@%d", ct, before, lastBefore, after, lastAfter)
		}
	}
	if ok, err := r.Allow(6); !ok || err != nil {
		t.Fatalf("limiter unusable after rejections: %v %v", ok, err)
	}
	if rs, err := New(3, 10); err != nil || rs.SelfCheck() != nil {
		t.Fatalf("SelfCheck: %v %v", rs.SelfCheck(), err)
	}
}

// Test_ConcurrentAllow: N goroutines at one t; admits <= limit == Accepted();
// a start channel fans out — no sleeps.
func Test_ConcurrentAllow(t *testing.T) {
	for _, c := range [][2]int64{{50, 1}, {100, 3}, {200, 50}} {
		r, _ := New(c[1], 10)
		start := make(chan struct{})
		var wg sync.WaitGroup
		var admit atomic.Int64
		for i := int64(0); i < c[0]; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				if ok, err := r.Allow(7); err != nil {
					t.Errorf("concurrent Allow: %v", err)
				} else if ok {
					admit.Add(1)
				}
			}()
		}
		close(start)
		wg.Wait()
		if a := admit.Load(); a > c[1] || int64(r.Accepted()) != a {
			t.Fatalf("admit=%d Accepted=%d limit=%d", a, r.Accepted(), c[1])
		}
	}
}
