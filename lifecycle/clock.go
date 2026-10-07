package lifecycle

import (
	"sync"
	"time"
)

// ManualClock is a deterministic, scriptable Clock for tests and the naive
// differential model. It is safe for concurrent use.
type ManualClock struct {
	mu  sync.Mutex
	now time.Time
}

// NewManualClock starts at t.
func NewManualClock(t time.Time) *ManualClock {
	return &ManualClock{now: t}
}

// Now returns the current instant.
func (c *ManualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Set moves the clock to t; t may be earlier than the previous value
// (clock regression is a supported, rare input).
func (c *ManualClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

// Advance moves the clock forward by d.
func (c *ManualClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// Regress moves the clock backward by d.
func (c *ManualClock) Regress(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(-d)
}

// ClockFunc adapts ManualClock to the Clock function type.
func (c *ManualClock) ClockFunc() Clock { return c.Now }
