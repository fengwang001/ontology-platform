package bucket

import (
	"errors"
	"testing"
	"time"
)

// clock is a manually advanced injected clock.
type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func TestContinuousRefillAndCap(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	b := New(10, 2, c.now) // 2 tokens/sec, cap 10
	if err := b.TryTake(10); err != nil {
		t.Fatalf("drain: %v", err)
	}
	c.advance(1500 * time.Millisecond) // +3 tokens
	if got := b.Balance(); got != 3 {
		t.Fatalf("balance after 1.5s = %v, want 3", got)
	}
	c.advance(250 * time.Millisecond) // +0.5 token
	if got := b.Balance(); got != 3.5 {
		t.Fatalf("balance after 1.75s = %v, want 3.5", got)
	}
	c.advance(10 * time.Second) // way past capacity
	if got := b.Balance(); got != 10 {
		t.Fatalf("balance capped = %v, want 10", got)
	}
}

func TestClockRewindDoesNotDrain(t *testing.T) {
	base := time.Unix(1000, 0)
	cur := base
	b := New(10, 5, func() time.Time { return cur })
	if err := b.TryTake(4); err != nil {
		t.Fatalf("take: %v", err)
	}
	cur = base.Add(-time.Hour) // rewind the clock
	if got := b.Balance(); got != 6 {
		t.Fatalf("balance after rewind = %v, want 6 (no drain)", got)
	}
	cur = base.Add(time.Second) // forward again: +5 from last mark
	if got := b.Balance(); got != 10 {
		t.Fatalf("balance after re-advance = %v, want 10 (capped)", got)
	}
}

func TestTryTakeFailuresLeaveBalanceUntouched(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	b := New(5, 0, c.now) // never refills
	if err := b.TryTake(3); err != nil {
		t.Fatalf("take 3: %v", err)
	}
	if err := b.TryTake(3); !errors.Is(err, ErrInsufficient) {
		t.Fatalf("take 3 again: got %v, want ErrInsufficient", err)
	}
	if got := b.Balance(); got != 2 {
		t.Fatalf("balance after failed take = %v, want 2", got)
	}
	if err := b.TryTake(6); !errors.Is(err, ErrExceedsCapacity) {
		t.Fatalf("take 6: got %v, want ErrExceedsCapacity", err)
	}
	if err := b.TryTake(0); !errors.Is(err, ErrInvalidAmount) {
		t.Fatalf("take 0: got %v, want ErrInvalidAmount", err)
	}
	if err := b.TryTake(-1); !errors.Is(err, ErrInvalidAmount) {
		t.Fatalf("take -1: got %v, want ErrInvalidAmount", err)
	}
	if got := b.Balance(); got != 2 {
		t.Fatalf("balance after invalid takes = %v, want 2", got)
	}
}

func TestGiveBackRollsBack(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	b := New(10, 0, c.now)
	if err := b.TryTake(4); err != nil {
		t.Fatalf("take: %v", err)
	}
	b.GiveBack(4)
	if got := b.Balance(); got != 10 {
		t.Fatalf("balance after giveback = %v, want 10", got)
	}
	b.GiveBack(100) // capped at capacity
	if got := b.Balance(); got != 10 {
		t.Fatalf("balance after over-giveback = %v, want 10", got)
	}
}

func TestReconfigureTruncatesButNeverTopsUp(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	b := New(10, 1, c.now)
	b.Reconfigure(4, 1) // shrink: truncate 10 -> 4
	if got := b.Balance(); got != 4 {
		t.Fatalf("balance after shrink = %v, want 4", got)
	}
	if err := b.TryTake(3); err != nil {
		t.Fatalf("take: %v", err)
	}
	b.Reconfigure(100, 1) // grow: balance stays 1, not topped up
	if got := b.Balance(); got != 1 {
		t.Fatalf("balance after grow = %v, want 1", got)
	}
	if got := b.Capacity(); got != 100 {
		t.Fatalf("capacity = %v, want 100", got)
	}
	if got := b.Rate(); got != 1 {
		t.Fatalf("rate = %v, want 1", got)
	}
}
