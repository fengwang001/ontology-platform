package bucket

import (
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func almostEq(a, b float64) bool {
	d := a - b
	return d > -1e-9 && d < 1e-9
}

func TestContinuousRefillAndCap(t *testing.T) {
	c := &fakeClock{t: time.Unix(1000, 0)}
	b := New(10, 4, c.now) // 4 tokens/sec, cap 10
	if !b.TryTake(8) {
		t.Fatal("expected take of 8 to succeed")
	}
	c.advance(500 * time.Millisecond) // +2 tokens, continuous not per-second
	if got := b.Balance(); !almostEq(got, 4) {
		t.Fatalf("balance = %v, want 4", got)
	}
	c.advance(10 * time.Second) // would be +40, capped at 10
	if got := b.Balance(); !almostEq(got, 10) {
		t.Fatalf("balance = %v, want capped 10", got)
	}
}

func TestClockBackwardsDoesNotDrain(t *testing.T) {
	c := &fakeClock{t: time.Unix(1000, 0)}
	b := New(10, 5, c.now)
	if !b.TryTake(6) {
		t.Fatal("take failed")
	}
	c.advance(time.Second) // +5 -> 9
	before := b.Balance()
	c.advance(-10 * time.Second) // clock jumps backwards
	if got := b.Balance(); !almostEq(got, before) {
		t.Fatalf("balance moved on backwards clock: %v -> %v", before, got)
	}
	if !b.TryTake(9) {
		t.Fatal("backwards clock must not destroy tokens")
	}
	if got := b.Balance(); !almostEq(got, 0) {
		t.Fatalf("balance = %v, want 0", got)
	}
}

func TestTryTakeAndRollback(t *testing.T) {
	c := &fakeClock{t: time.Unix(1000, 0)}
	b := New(5, 1, c.now)
	if b.TryTake(6) {
		t.Fatal("take beyond capacity must fail")
	}
	if !b.TryTake(5) {
		t.Fatal("take of full balance failed")
	}
	if b.TryTake(1) {
		t.Fatal("take from empty bucket must fail")
	}
	b.Rollback(3)
	if got := b.Balance(); !almostEq(got, 3) {
		t.Fatalf("balance = %v, want 3", got)
	}
	b.Rollback(100) // capped at capacity
	if got := b.Balance(); !almostEq(got, 5) {
		t.Fatalf("balance = %v, want capped 5", got)
	}
}

func TestUpdateShrinkTruncatesGrowKeeps(t *testing.T) {
	c := &fakeClock{t: time.Unix(1000, 0)}
	b := New(10, 1, c.now)
	b.Update(4, 1) // shrink: 10 -> truncated to 4
	if got := b.Balance(); !almostEq(got, 4) {
		t.Fatalf("balance = %v, want truncated 4", got)
	}
	b.Update(20, 1) // grow: balance stays 4, no free top-up
	if got := b.Balance(); !almostEq(got, 4) {
		t.Fatalf("balance = %v, want unchanged 4", got)
	}
	if got := b.Capacity(); !almostEq(got, 20) {
		t.Fatalf("capacity = %v, want 20", got)
	}
}
