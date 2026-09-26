package api

import (
	"errors"
	"math/rand"
	"testing"
)

// Invariant 1: Decide equals a naive rescan ("Commit iff every
// participant voted yes") over random votes in random arrival order.
func TestDecideMatchesNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for _, n := range []int{1, 2, 3, 5, 8, 13, 64} {
		for trial := 0; trial < 20; trial++ {
			c, naive := New(n), Commit
			for _, p := range rng.Perm(n) {
				yes := rng.Intn(2) == 0
				if !yes {
					naive = Abort
				}
				if err := c.Vote(p, yes); err != nil {
					t.Fatal(err)
				}
			}
			if got, err := c.Decide(); err != nil || got != naive {
				t.Fatalf("n=%d: Decide=%v,%v want %v", n, got, err, naive)
			}
		}
	}
}

// Invariant 2: Commit iff all n voted yes; any no or missing vote can
// never commit.
func TestUnanimousRequired(t *testing.T) {
	cases := []struct {
		n, noAt, skip int // -1 = none
		want          Decision
	}{
		{3, -1, -1, Commit}, {3, 0, -1, Abort}, {3, 2, -1, Abort},
		{1, -1, -1, Commit}, {1, 0, -1, Abort}, {5, 3, -1, Abort},
	}
	for _, tc := range cases {
		c := New(tc.n)
		for p := 0; p < tc.n; p++ {
			if p != tc.skip {
				if err := c.Vote(p, p != tc.noAt); err != nil {
					t.Fatal(err)
				}
			}
		}
		if got, err := c.Decide(); err != nil || got != tc.want {
			t.Errorf("%+v: got %v,%v", tc, got, err)
		}
	}
	c := New(3) // missing vote: Decide refuses, never commits
	c.Vote(0, true)
	c.Vote(1, true)
	if d, err := c.Decide(); !errors.Is(err, ErrNotAllVoted) || d != Undecided {
		t.Errorf("partial: got %v,%v want Undecided,ErrNotAllVoted", d, err)
	}
}

// Invariant 3: a cast vote cannot change.
func TestVoteImmutable(t *testing.T) {
	c := New(2)
	if err := c.Vote(0, false); err != nil {
		t.Fatal(err)
	}
	for _, again := range []bool{true, false} { // flip or repeat: rejected
		if err := c.Vote(0, again); !errors.Is(err, ErrAlreadyVoted) {
			t.Fatalf("re-vote(%v): %v", again, err)
		}
	}
	if y, n := c.Counts(); y != 0 || n != 1 {
		t.Fatalf("counts=%d/%d want 0/1", y, n)
	}
	c.Vote(1, true)
	if d, _ := c.Decide(); d != Abort { // the original no still stands
		t.Fatalf("Decide=%v want Abort (vote 0 stays no)", d)
	}
}

// Invariant 4: every rejection is distinct and leaves no trace.
func TestRejectionsLeaveStateUnchanged(t *testing.T) {
	c := New(3)
	c.Vote(0, true)
	_, eDecide := New(2).Decide()
	rej := []error{c.Vote(-1, true), c.Vote(3, true), c.Vote(0, false), eDecide}
	want := []error{ErrOutOfRange, ErrOutOfRange, ErrAlreadyVoted, ErrNotAllVoted}
	for i := range rej {
		if !errors.Is(rej[i], want[i]) {
			t.Fatalf("rejection %d: got %v want %v", i, rej[i], want[i])
		}
	}
	seen := map[error]bool{}
	for _, e := range []error{ErrOutOfRange, ErrAlreadyVoted, ErrNotAllVoted} {
		if seen[e] {
			t.Fatalf("sentinel %v not distinct", e)
		}
		seen[e] = true
	}
	if y, n := c.Counts(); y != 1 || n != 0 { // nothing stuck
		t.Fatalf("counts=%d/%d want 1/0", y, n)
	}
	c.Vote(1, true)
	c.Vote(2, true) // still fully usable afterwards
	if d, err := c.Decide(); err != nil || d != Commit {
		t.Fatalf("after rejections: Decide=%v,%v want Commit", d, err)
	}
}
