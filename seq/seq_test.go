package seq

import (
	"fmt"
	"math/rand"
	"runtime"
	"testing"
)

func TestInitialAndNewSizes(t *testing.T) {
	for _, n := range []int{1, 2, 7, 100} {
		c := NewCore(n)
		if c.Seq() != 0 {
			t.Fatalf("n=%d: initial seq = %d, want 0", n, c.Seq())
		}
		if got := c.Snapshot(); len(got) != n {
			t.Fatalf("n=%d: snapshot len = %d", n, len(got))
		} else {
			for i, x := range got {
				if x != 0 {
					t.Fatalf("n=%d: element %d = %d, want 0", n, i, x)
				}
			}
		}
	}
}

// TestTerminals: every Snapshot equals the terminal of some completed
// Begin/Commit cycle, never an unseen value. Random subset, random order.
func TestTerminals(t *testing.T) {
	for _, n := range []int{1, 2, 8, 64} {
		c := NewCore(n)
		rng := rand.New(rand.NewSource(int64(n) * 7))
		terms := map[string]bool{fmt.Sprint(c.Snapshot()): true}
		for round := 1; round <= 40; round++ {
			sh := c.Begin()
			order := rng.Perm(n)
			for _, idx := range order[:1+rng.Intn(n)] {
				sh[idx] = int64(round) // elements written one by one, in random order
			}
			c.Commit(sh)
			terms[fmt.Sprint(c.Snapshot())] = true
		}
		for i := 0; i < 300; i++ {
			if s := fmt.Sprint(c.Snapshot()); !terms[s] {
				t.Fatalf("n=%d: snapshot %s is not any committed terminal", n, s)
			}
		}
	}
}

// TestVersionMonotonic: odd inside a write, +2 per committed cycle, and reads
// never advance seq.
func TestVersionMonotonic(t *testing.T) {
	c := NewCore(3)
	for round := 1; round <= 10; round++ {
		before := c.Seq()
		sh := c.Begin()
		if s := c.Seq(); s != before+1 || s&1 == 0 {
			t.Fatalf("round %d: inside write seq = %d, want odd %d", round, c.Seq(), before+1)
		}
		sh[0], sh[1], sh[2] = int64(round), int64(round), int64(round)
		c.Commit(sh)
		if c.Seq() != before+2 || c.Seq()&1 != 0 {
			t.Fatalf("round %d: after commit seq = %d, want even %d", round, c.Seq(), before+2)
		}
		if c.Snapshot(); c.Seq() != before+2 {
			t.Fatalf("round %d: Snapshot changed seq to %d", round, c.Seq())
		}
	}
}

// TestOneElementWriteCount: touching exactly one element always counts one
// element write, independent of m, proving in-place O(1) publication.
func TestOneElementWriteCount(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		c := NewCore(m)
		rng := rand.New(rand.NewSource(int64(m)))
		for round := int64(1); round <= 20; round++ {
			idx := rng.Intn(m)
			sh := c.Begin()
			sh[idx] = round
			c.Commit(sh)
			if c.writes != 1 {
				t.Fatalf("m=%d round=%d: element writes = %d, want 1", m, round, c.writes)
			}
			if got := c.Snapshot()[idx]; got != round {
				t.Fatalf("m=%d: element %d = %d, want %d", m, idx, got, round)
			}
		}
	}
}

// TestRollbackLeavesEvenSeq: a panicked-style aborted write must restore an
// even seq and publish nothing, or readers would retry forever.
func TestRollbackLeavesEvenSeq(t *testing.T) {
	c := NewCore(2)
	before := c.Seq()
	sh := c.Begin()
	sh[0] = 9
	c.Rollback()
	if c.Seq() != before+2 || c.Seq()&1 != 0 {
		t.Fatalf("after rollback seq = %d, want even %d", c.Seq(), before+2)
	}
	if s := fmt.Sprint(c.Snapshot()); s != "[0 0]" {
		t.Fatalf("after rollback array = %s, want [0 0]", s)
	}
}

// TestReaderWaitsOutOddPhase: a Snapshot started while seq is odd must not
// return until the commit; then it sees the committed value.
func TestReaderWaitsOutOddPhase(t *testing.T) {
	c := NewCore(2)
	sh := c.Begin()
	got := make(chan []int64, 1)
	go func() { got <- c.Snapshot() }()
	for i := 0; i < 200; i++ {
		runtime.Gosched()
		select {
		case s := <-got:
			t.Fatalf("reader returned during odd phase with %v", s)
		default:
		}
	}
	sh[0], sh[1] = 4, 4
	c.Commit(sh)
	if s := <-got; fmt.Sprint(s) != "[4 4]" {
		t.Fatalf("reader got %v, want [4 4]", s)
	}
}
