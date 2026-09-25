package wcount

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

type ref struct {
	high, acc map[string]int64
	drop      int64
}

func (s *ref) feed(K int64, e Event) {
	h, seen := s.high[e.Key]
	switch {
	case !seen:
		s.high[e.Key] = e.Seq
	case e.Seq > h:
		s.high[e.Key] = e.Seq
	case h-e.Seq > K: // older than high-K: drop, high untouched
		s.drop++
		return
	}
	s.acc[e.Key]++
}

// TestNaiveEquivalence pins invariant 1 under randomized arrival orders.
func TestNaiveEquivalence(t *testing.T) {
	for i, tc := range []struct {
		seed int64
		K    int64
	}{{1, 3}, {2, 0}, {3, 7}, {4, 1}} {
		c, _ := New(tc.K, 32)
		r, s := rand.New(rand.NewSource(tc.seed)), &ref{map[string]int64{}, map[string]int64{}, 0}
		for n := 0; n < 2000; n++ {
			e := Event{Key: fmt.Sprintf("k%d", r.Intn(6)), Seq: int64(r.Intn(40)) - 5}
			if err := c.Feed([]Event{e}); err != nil {
				t.Fatal(err)
			}
			s.feed(tc.K, e)
			for k, h := range s.high {
				if hh, _ := c.High(k); hh != h || c.Accepted(k) != s.acc[k] {
					t.Fatalf("case %d step %d key %s diverged", i, n, k)
				}
			}
			if c.Dropped() != s.drop {
				t.Fatalf("case %d step %d drop", i, n)
			}
		}
	}
}

// TestWindowBoundaries pins invariant 2: closed floor, high never retreats.
func TestWindowBoundaries(t *testing.T) {
	c, _ := New(3, 1)
	steps := []struct{ seq, high, acc, drop int64 }{
		{10, 10, 1, 0}, {8, 10, 2, 0}, {12, 12, 3, 0}, {5, 12, 3, 1},
		{8, 12, 3, 2}, {11, 12, 4, 2}, {9, 12, 5, 2}, {7, 12, 5, 3},
	}
	for i, st := range steps {
		if err := c.Feed([]Event{{Key: "a", Seq: st.seq}}); err != nil {
			t.Fatal(err)
		}
		h, _ := c.High("a")
		if h != st.high || c.Accepted("a") != st.acc || c.Dropped() != st.drop {
			t.Fatalf("step %d high=%d acc=%d drop=%d", i, h, c.Accepted("a"), c.Dropped())
		}
	}
	b, _ := New(3, 1)
	_ = b.Feed([]Event{{Key: "a", Seq: 10}})
	wantDrop := []bool{false, true, false} // ==high-K accept; high-K-1 drop
	for i, seq := range []int64{7, 6, 9} {
		d0 := b.Dropped()
		_ = b.Feed([]Event{{Key: "a", Seq: seq}})
		h, _ := b.High("a")
		if (b.Dropped() > d0) != wantDrop[i] || h != 10 {
			t.Fatalf("seq %d boundary wrong", seq)
		}
	}
}

// TestCountersMonotonic pins invariant 3.
func TestCountersMonotonic(t *testing.T) {
	c, _ := New(2, 8)
	r, d0 := rand.New(rand.NewSource(99)), int64(0)
	a0 := map[string]int64{}
	for n := 0; n < 1000; n++ {
		k := fmt.Sprintf("k%d", r.Intn(5))
		_ = c.Feed([]Event{{Key: k, Seq: int64(r.Intn(30))}})
		if c.Accepted(k) < a0[k] || c.Dropped() < d0 {
			t.Fatalf("counters retreated at step %d", n)
		}
		a0[k], d0 = c.Accepted(k), c.Dropped()
	}
}

// TestFeedAtomicity pins invariant 4 and the three distinct sentinels.
func TestFeedAtomicity(t *testing.T) {
	c, _ := New(3, 2)
	if err := c.Feed([]Event{{Key: "x", Seq: 1}}); err != nil {
		t.Fatal(err)
	}
	try := func(evs []Event, want error) {
		hx, ax, dx := c.high["x"].High, c.Accepted("x"), c.Dropped()
		if err := c.Feed(evs); !errors.Is(err, want) {
			t.Fatalf("err=%v want %v", err, want)
		}
		if c.high["x"].High != hx || c.Accepted("x") != ax || c.Dropped() != dx {
			t.Fatalf("rejected batch changed state")
		}
	}
	try([]Event{{Key: "", Seq: 1}}, ErrEmptyKey)
	try([]Event{{Key: "x", Seq: 2}, {Key: "", Seq: 1}}, ErrEmptyKey)
	try([]Event{{Key: "y", Seq: 1}, {Key: "z", Seq: 1}}, ErrTooManyKeys)
	if _, ok := c.high["y"]; ok {
		t.Fatal("new key leaked")
	}
	if err := c.Feed([]Event{{Key: "x", Seq: 5}}); err != nil || c.Accepted("x") != 2 {
		t.Fatal("counter unusable after rejected batches")
	}
	for _, tc := range []struct{ k, m int64 }{{-1, 1}, {3, 0}, {0, -1}} {
		if _, err := New(tc.k, int(tc.m)); !errors.Is(err, ErrInvalidArgs) {
			t.Fatalf("New(%d,%d)=%v", tc.k, tc.m, err)
		}
	}
	if errors.Is(ErrEmptyKey, ErrTooManyKeys) || errors.Is(ErrEmptyKey, ErrInvalidArgs) {
		t.Fatal("sentinel errors must be mutually distinct")
	}
}

func TestProbeNotLinear(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		c, _ := New(3, m+10)
		batch := make([]Event, m)
		for i := range batch {
			batch[i] = Event{Key: fmt.Sprintf("k%05d", i), Seq: 1}
		}
		if err := c.Feed(batch); err != nil {
			t.Fatal(err)
		}
		if err := c.Feed([]Event{{Key: fmt.Sprintf("k%05d", m/2), Seq: 100}}); err != nil {
			t.Fatal(err)
		}
		if c.probe > 1 {
			t.Fatalf("m=%d: examined %d keys, want <= 1", m, c.probe)
		}
	}
}
