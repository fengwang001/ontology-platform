package api

import (
	"errors"
	"math/rand"
	"sync"
	"testing"
)

func ev(k int, ts int64) Event { return Event{Key: k, TS: ts} }

// naiveRef rescans accepted events; equivalent to the last[] rule.
func naiveRef(all []Event, W int64) int {
	T := all[len(all)-1].TS
	seen := map[int]bool{}
	for _, e := range all {
		if e.TS > T-W && e.TS <= T {
			seen[e.Key] = true
		}
	}
	return len(seen)
}

// TestNaiveConsistent pins invariant 1 (and exercises 2): random
// non-decreasing streams fed in random-sized batches (incl. size 1)
// must match an exact naive rescan at every prefix.
func TestNaiveConsistent(t *testing.T) {
	for _, W := range []int64{1, 3, 10, 50} {
		for _, b := range []int64{1, W} {
			rng := rand.New(rand.NewSource(W*7 + b))
			all, ts := []Event{}, int64(0)
			for range 300 {
				ts += rng.Int63n(4)
				all = append(all, ev(rng.Intn(12), ts))
			}
			c, _ := New(W, b)
			for i := 0; i < len(all); {
				n := 1 + rng.Intn(5)
				if i+n > len(all) {
					n = len(all) - i
				}
				err := c.Feed(all[i : i+n])
				if err != nil || c.Distinct() != naiveRef(all[:i+n], W) {
					t.Fatalf("W=%d b=%d i=%d: got %d naive %d err %v", W, b, i, c.Distinct(), naiveRef(all[:i+n], W), err)
				}
				i += n
			}
		}
	}
}

// TestBoundarySemantics pins invariant 3; closedLeft/openRight are the
// off-by-one wrong results (-1 = that wrong rule is not targeted).
func TestBoundarySemantics(t *testing.T) {
	cases := []struct {
		name                        string
		W, b                        int64
		evs                         []Event
		want, closedLeft, openRight int
	}{
		{"last==T-W excluded", 10, 5, []Event{ev(10, 3), ev(11, 13)}, 1, 2, 0},
		{"TS==T included", 10, 5, []Event{ev(1, 0), ev(4, 13)}, 1, -1, 0},
		{"W=1 b=1 edges", 1, 1, []Event{ev(1, 0), ev(2, 1)}, 1, 2, 0},
		{"both edges T=5", 5, 5, []Event{ev(7, 0), ev(8, 5)}, 1, 2, 0},
		{"NOTES six steps", 10, 5, []Event{ev(1, 1), ev(2, 3), ev(5, 4), ev(1, 6), ev(3, 11), ev(4, 13)}, 4, 5, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := New(tc.W, tc.b)
			if err := c.Feed(tc.evs); err != nil || c.Distinct() != tc.want {
				t.Fatalf("got %d want %d (%v)", c.Distinct(), tc.want, err)
			}
			if (tc.closedLeft != -1 && tc.closedLeft == tc.want) ||
				(tc.openRight != -1 && tc.openRight == tc.want) {
				t.Fatal("a wrong-implementation result equals the correct one")
			}
		})
	}
}

// TestRejectLeavesNoTrace pins invariant 4: four distinct sentinels;
// a refused batch leaves no trace and the counter stays usable.
func TestRejectLeavesNoTrace(t *testing.T) {
	for _, tc := range []struct {
		W, b int64
		err  error
	}{
		{0, 1, ErrInvalidWindow}, {-5, 1, ErrInvalidWindow},
		{10, 0, ErrInvalidBucket}, {10, -1, ErrInvalidBucket}, {10, 11, ErrInvalidBucket},
	} {
		if _, err := New(tc.W, tc.b); !errors.Is(err, tc.err) {
			t.Fatalf("New(%d,%d)=%v", tc.W, tc.b, err)
		}
	}
	c, _ := New(10, 5)
	if err := c.Feed([]Event{ev(1, 1), ev(2, 3)}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		evs []Event
		err error
	}{
		{[]Event{ev(3, 4), ev(-1, 5)}, ErrInvalidEvent},
		{[]Event{ev(3, -1)}, ErrInvalidEvent},
		{[]Event{ev(3, 2)}, ErrTSRollback},
		{[]Event{ev(3, 4), ev(3, 3)}, ErrTSRollback},
	} {
		before := c.Distinct()
		if !errors.Is(c.Feed(tc.evs), tc.err) || c.Distinct() != before {
			t.Fatalf("batch %v misrejected or left a trace", tc.evs)
		}
	}
	if err := c.Feed([]Event{ev(9, 9)}); err != nil || c.Distinct() != 3 {
		t.Fatal("counter not usable after rejection")
	}
}

// TestSelfCheck exercises the callable built-in self-verification.
func TestSelfCheck(t *testing.T) {
	c, err := New(10, 5)
	if err != nil || !c.SelfCheck() {
		t.Fatal("SelfCheck must pass")
	}
}

// TestConcurrentDistinct: after feeding, N goroutines only read
// Distinct and must all get the identical value. No sleeps.
func TestConcurrentDistinct(t *testing.T) {
	c, _ := New(10, 5)
	rng := rand.New(rand.NewSource(99))
	var ts int64
	for range 500 {
		ts += rng.Int63n(3)
		if err := c.Feed([]Event{ev(rng.Intn(30), ts)}); err != nil {
			t.Fatal(err)
		}
	}
	want, res := c.Distinct(), make([]int, 16)
	var wg sync.WaitGroup
	for g := range res {
		wg.Add(1)
		go func(i int) { defer wg.Done(); res[i] = c.Distinct() }(g)
	}
	wg.Wait()
	for g, v := range res {
		if v != want {
			t.Fatalf("reader %d got %d want %d", g, v, want)
		}
	}
}
