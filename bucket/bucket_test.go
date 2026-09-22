package bucket

import (
	"sync"
	"testing"
	"time"
)

// fakeClock is a manually advanced, mutex-protected clock.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Unix(1_700_000_000, 0)}
}

func (c *fakeClock) Time() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func almostEqual(a, b float64) bool {
	d := a - b
	return d > -1e-9 && d < 1e-9
}

func TestContinuousRefillAndCap(t *testing.T) {
	c := newFakeClock()
	b := New(10, 2, c.Time) // 2 tokens/sec, cap 10
	if !b.TryTake(10) {
		t.Fatal("full bucket should allow taking 10")
	}
	c.Advance(1500 * time.Millisecond) // +3 tokens
	if got := b.Balance(); !almostEqual(got, 3) {
		t.Fatalf("balance after 1.5s = %v, want 3", got)
	}
	c.Advance(10 * time.Second) // +20, capped at 10
	if got := b.Balance(); !almostEqual(got, 10) {
		t.Fatalf("balance should cap at capacity, got %v", got)
	}
}

func TestClockRewindDoesNotDeduct(t *testing.T) {
	c := newFakeClock()
	b := New(10, 5, c.Time)
	if !b.TryTake(4) {
		t.Fatal("take 4")
	}
	c.Advance(time.Second) // 6 + 5 = 10 (capped)
	if got := b.Balance(); !almostEqual(got, 10) {
		t.Fatalf("after 1s refill got %v, want 10", got)
	}
	c.Advance(-5 * time.Second) // rewind: must be ignored
	if got := b.Balance(); !almostEqual(got, 10) {
		t.Fatalf("rewind must not deduct, got %v", got)
	}
	c.Advance(-time.Hour)
	if got := b.Balance(); !almostEqual(got, 10) {
		t.Fatalf("further rewind must not deduct, got %v", got)
	}
}

func TestTryTakeAndRefund(t *testing.T) {
	c := newFakeClock()
	b := New(5, 0, c.Time) // no refill
	if b.TryTake(6) {
		t.Fatal("taking more than balance must fail")
	}
	if !b.TryTake(5) {
		t.Fatal("take all")
	}
	if b.TryTake(1) {
		t.Fatal("empty bucket must reject")
	}
	b.Refund(5)
	if got := b.Balance(); !almostEqual(got, 5) {
		t.Fatalf("after refund got %v, want 5", got)
	}
	b.Refund(100) // refund caps at capacity
	if got := b.Balance(); !almostEqual(got, 5) {
		t.Fatalf("refund must cap at capacity, got %v", got)
	}
}

func TestSetLimitTruncatesAndNeverTopsUp(t *testing.T) {
	c := newFakeClock()
	b := New(10, 1, c.Time)
	b.SetLimit(4, 1) // shrink: truncate to 4
	if got := b.Balance(); !almostEqual(got, 4) {
		t.Fatalf("shrink should truncate to 4, got %v", got)
	}
	if !b.TryTake(3) {
		t.Fatal("take 3")
	}
	b.SetLimit(100, 1) // grow: balance stays 1, no top-up
	if got := b.Balance(); !almostEqual(got, 1) {
		t.Fatalf("grow must not top up, got %v", got)
	}
	c.Advance(2 * time.Second) // new rate applies
	if got := b.Balance(); !almostEqual(got, 3) {
		t.Fatalf("refill at new rate, got %v, want 3", got)
	}
}
