package coord

import (
	"sync"
	"sync/atomic"
	"testing"
)

// TestDecideCheckedBounded proves Decide and Recover judge from the
// incrementally maintained yes/no counters: the number of participants
// examined (unexported field checked) stays at a small constant and
// does not grow with the participant count m.
func TestDecideCheckedBounded(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		c := New(m)
		for p := 0; p < m; p++ {
			if err := c.Vote(p, true); err != nil {
				t.Fatalf("m=%d vote %d: %v", m, p, err)
			}
		}
		if d, err := c.Decide(); err != nil || d != Commit {
			t.Fatalf("m=%d: Decide = %v, %v, want Commit", m, d, err)
		}
		if c.checked != 0 {
			t.Errorf("m=%d: Decide examined %d participants, want 0", m, c.checked)
		}

		r := New(m) // Recover on a partial vote must also be O(1).
		for p := 0; p < m/2; p++ {
			if err := r.Vote(p, true); err != nil {
				t.Fatalf("m=%d vote %d: %v", m, p, err)
			}
		}
		if d := r.Recover(); d != Abort {
			t.Fatalf("m=%d: Recover = %v, want Abort", m, d)
		}
		if r.checked != 0 {
			t.Errorf("m=%d: Recover examined %d participants, want 0", m, r.checked)
		}
	}
}

// TestRecoverRules: keep a made decision; complete votes decide by the
// rule; missing votes abort the in-flight transaction.
func TestRecoverRules(t *testing.T) {
	cases := []struct {
		votes []bool // shorter than n = missing votes
		want  Decision
	}{
		{[]bool{true, true, true}, Commit},
		{[]bool{true, false, true}, Abort},
		{[]bool{true, true}, Abort},
		{nil, Abort},
	}
	for _, tc := range cases {
		c := New(3)
		for p, v := range tc.votes {
			c.Vote(p, v)
		}
		if got := c.Recover(); got != tc.want {
			t.Errorf("votes=%v: Recover=%v want %v", tc.votes, got, tc.want)
		}
	}
	c := New(2) // a made decision survives Recover
	c.Vote(0, true)
	c.Vote(1, true)
	if d, _ := c.Decide(); d != Commit {
		t.Fatal("setup: want Commit")
	}
	if got := c.Recover(); got != Commit {
		t.Errorf("Recover after Commit = %v", got)
	}
}

// TestConcurrentVotesCommit: M goroutines vote distinct participants
// (all yes) while a reader samples the voted count (monotonic);
// concurrent Decide and Recover then all agree on Commit. No sleeps.
func TestConcurrentVotesCommit(t *testing.T) {
	const m = 256
	c := New(m)
	done := make(chan struct{})
	var decreased atomic.Bool
	var readers sync.WaitGroup
	readers.Add(1)
	go func() {
		defer readers.Done()
		for prev := 0; ; {
			select {
			case <-done:
				return
			default:
			}
			y, n := c.Counts()
			if y+n < prev {
				decreased.Store(true)
			}
			prev = y + n
		}
	}()
	var voters sync.WaitGroup
	for p := 0; p < m; p++ {
		voters.Add(1)
		go func(p int) {
			defer voters.Done()
			if err := c.Vote(p, true); err != nil {
				t.Errorf("vote %d: %v", p, err)
			}
		}(p)
	}
	voters.Wait()
	close(done)
	readers.Wait()
	if decreased.Load() {
		t.Fatal("voted count decreased")
	}
	var deciders sync.WaitGroup // Decide and Recover race; all see Commit
	for i := 0; i < 32; i++ {
		deciders.Add(1)
		go func(i int) {
			defer deciders.Done()
			if i%2 == 0 {
				if d, err := c.Decide(); err != nil || d != Commit {
					t.Errorf("Decide=%v,%v want Commit", d, err)
				}
			} else if d := c.Recover(); d != Commit {
				t.Errorf("Recover=%v want Commit", d)
			}
		}(i)
	}
	deciders.Wait()
}
