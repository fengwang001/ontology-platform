// Package clock provides an injectable nanosecond-precision clock.
package clock

import "sync"

// Clock returns the current time in nanoseconds since an arbitrary epoch.
type Clock interface {
	Now() int64
}

// Manual is a manually driven Clock safe for concurrent use.
type Manual struct {
	t  int64
	mu sync.Mutex
}

// NewManual creates a manual clock at now.
func NewManual(now int64) *Manual {
	return &Manual{t: now}
}

// Now returns the current time in nanoseconds.
func (c *Manual) Now() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

// Set moves the clock to t; backward moves are allowed here so that the
// timer layer can distinguish and reject them atomically.
func (c *Manual) Set(t int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = t
}

// Add advances the clock by d nanoseconds (d may be negative).
func (c *Manual) Add(d int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t += d
}
