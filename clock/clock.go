// Package clock provides an injectable time source.
package clock

import (
	"sync"
	"time"
)

// Clock is the time interface used by every component.
type Clock interface {
	Now() time.Time
	Sleep(d time.Duration)
}

// RealClock reports wall-clock time.
type RealClock struct{}

// Now returns the current wall-clock time.
func (RealClock) Now() time.Time { return time.Now() }

// Sleep blocks for d.
func (RealClock) Sleep(d time.Duration) { time.Sleep(d) }

// FakeClock is a manually driven, concurrency-safe clock.
type FakeClock struct {
	mu sync.Mutex
	t  time.Time
}

// NewFake returns a FakeClock at t.
func NewFake(t time.Time) *FakeClock { return &FakeClock{t: t} }

// Now returns the current fake time.
func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

// Sleep advances the clock by d (it never blocks real time).
func (c *FakeClock) Sleep(d time.Duration) { c.Advance(d) }

// Advance moves the clock forward by d; panics on negative durations
// (use Set to model rollback deliberately).
func (c *FakeClock) Advance(d time.Duration) {
	if d < 0 {
		panic("clock: negative advance; use Set to inject rollback")
	}
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// Set assigns the clock time, allowing backwards jumps (rollback injection).
func (c *FakeClock) Set(t time.Time) {
	c.mu.Lock()
	c.t = t
	c.mu.Unlock()
}
