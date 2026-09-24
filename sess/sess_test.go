package sess

import (
	"errors"
	"math/rand"
	"reflect"
	"sort"
	"testing"

	"ontology/evt"
)

func eq(got, want []Session) bool { return reflect.DeepEqual(got, want) }

// TestSixSteps pins the six-row derivation table in NOTES.md (gap=10).
func TestSixSteps(t *testing.T) {
	want := [][]Session{
		{{100, 100, 1}},
		{{100, 105, 2}},
		{{100, 105, 2}, {130, 130, 1}},
		{{100, 105, 2}, {130, 135, 2}},
		{{100, 105, 2}, {118, 118, 1}, {130, 135, 2}},
		{{100, 105, 3}, {118, 118, 1}, {130, 135, 2}},
	}
	s := NewSet(10, 0)
	for i, ts := range []int64{100, 105, 130, 135, 118, 100} {
		if err := s.Add(evt.Event{Key: "k", TS: ts}); err != nil {
			t.Fatal(err)
		}
		if got := s.Sessions("k"); !eq(got, want[i]) {
			t.Fatalf("step %d: got %v want %v", i+1, got, want[i])
		}
	}
	s15 := NewSet(15, 0) // 118 bridges both (dist 13 and 12, both <=15)
	for _, ts := range []int64{100, 105, 130, 135, 118, 100} {
		if err := s15.Add(evt.Event{Key: "k", TS: ts}); err != nil {
			t.Fatal(err)
		}
	}
	if got := s15.Sessions("k"); !eq(got, []Session{{100, 135, 6}}) {
		t.Fatalf("gap=15 bridge: got %v", got)
	}
}

// oracle recomputes sessions from a sorted full rescan of the multiset.
func oracle(ts []int64, gap int64) []Session {
	cp := append([]int64(nil), ts...)
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	var out []Session
	for _, x := range cp {
		if n := len(out); n > 0 && x-out[n-1].End <= gap {
			out[n-1].End, out[n-1].N = x, out[n-1].N+1
		} else {
			out = append(out, Session{x, x, 1})
		}
	}
	return out
}

// TestRecomputeEquivalence: invariants 1 and 2 over many shuffled feeds.
func TestRecomputeEquivalence(t *testing.T) {
	for si, sp := range []struct{ n, span int64 }{{30, 50}, {100, 200}, {300, 60}, {500, 1000}} {
		rng := rand.New(rand.NewSource(int64(si + 1)))
		ts := make([]int64, sp.n)
		for i := range ts {
			ts[i] = rng.Int63n(sp.span) // duplicates included on purpose
		}
		want := oracle(ts, 10)
		for round := 0; round < 8; round++ {
			s := NewSet(10, 0)
			for _, p := range rng.Perm(len(ts)) {
				if err := s.Add(evt.Event{Key: "k", TS: ts[p]}); err != nil {
					t.Fatal(err)
				}
			}
			if got := s.Sessions("k"); !eq(got, want) {
				t.Fatalf("spec %d round %d:\n got %v\nwant %v", si, round, got, want)
			}
		}
	}
}

// TestCanonicalForm pins invariant 3 directly on random contents.
func TestCanonicalForm(t *testing.T) {
	for seed := int64(1); seed <= 20; seed++ {
		rng := rand.New(rand.NewSource(seed))
		gap := int64(1 + rng.Intn(20))
		s := NewSet(gap, 0)
		for i := 0; i < 200; i++ {
			if err := s.Add(evt.Event{Key: "k", TS: rng.Int63n(400) - 200}); err != nil {
				t.Fatal(err)
			}
		}
		got := s.Sessions("k")
		for i, c := range got {
			if c.Start > c.End || c.N <= 0 || (i > 0 && got[i-1].Start >= c.Start) {
				t.Fatalf("not canonical: %v", got)
			}
			if i > 0 && c.Start-got[i-1].End <= gap {
				t.Fatalf("adjacent gap not strictly > gap: %v then %v", got[i-1], c)
			}
		}
	}
}

// TestCompareBudget: a constant-touch insert must not scan all m sessions.
func TestCompareBudget(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		s := NewSet(10, m+5)
		for j := 0; j < m; j++ { // sessions at 0,20,40,...: mutually >10 apart
			if err := s.Add(evt.Event{Key: "k", TS: int64(20 * j)}); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Add(evt.Event{Key: "k", TS: 1}); err != nil { // touches only [0,0]
			t.Fatal(err)
		}
		if s.lastCmp > 2+1 { // bound: const 2 + sessions actually merged (1)
			t.Fatalf("m=%d: compared %d sessions, expected O(1)", m, s.lastCmp)
		}
	}
}

// TestRejectedLeavesNoTrace pins invariant 4 at the sess layer.
func TestRejectedLeavesNoTrace(t *testing.T) {
	s := NewSet(10, 1)
	if err := s.Add(evt.Event{Key: "k", TS: 0}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		e   evt.Event
		err error
	}{
		{evt.Event{Key: "k", TS: 100}, ErrTooManySessions},
		{evt.Event{Key: "", TS: 0}, evt.ErrInvalidEvent},
	} {
		before := s.Sessions("k")
		if err := s.Add(c.e); !errors.Is(err, c.err) {
			t.Fatalf("err = %v, want %v", err, c.err)
		}
		if got := s.Sessions("k"); !eq(got, before) {
			t.Fatalf("state changed: before %v after %v", before, got)
		}
	}
	if err := s.Add(evt.Event{Key: "k", TS: 5}); err != nil || // still usable, merges
		!eq(s.Sessions("k"), []Session{{0, 5, 2}}) {
		t.Fatalf("set unusable after rejection: %v %v", s.Sessions("k"), err)
	}
}
