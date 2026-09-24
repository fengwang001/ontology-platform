// Package chunker decides chunk boundaries as a pure function of the byte
// stream and an injected clock, never of upstream call boundaries.
package chunker

import (
	"errors"
	"time"
)

// Clock supplies time; production and tests inject their own.
type Clock interface {
	Now() time.Time
}

// ClockFunc adapts a function to Clock.
type ClockFunc func() time.Time

// Now implements Clock.
func (f ClockFunc) Now() time.Time { return f() }

// State is the chunker's restorable state.
type State struct {
	Pending int       // bytes accumulated in the currently open chunk
	Open    time.Time // clock reading at the first pending byte
}

// Chunker splits a byte stream into chunks of [Min, Max] bytes, aggregating
// stragglers below Min until Window elapses since the chunk opened.
type Chunker struct {
	min    int
	max    int
	window time.Duration
	clock  Clock
	st     State
}

// New builds a Chunker. min>=1, max>=min, window>=0, clock non-nil.
func New(min, max int, window time.Duration, clock Clock) (*Chunker, error) {
	if min < 1 || max < min || window < 0 || clock == nil {
		return nil, errors.New("chunker: invalid configuration")
	}
	return &Chunker{min: min, max: max, window: window, clock: clock}, nil
}

// Feed consumes p and returns the sizes of chunks closed by these bytes.
// Boundaries depend only on the byte stream and per-byte clock readings.
func (c *Chunker) Feed(p []byte) []int {
	var sizes []int
	for range p {
		now := c.clock.Now()
		if c.st.Pending == 0 {
			c.st.Open = now
		}
		c.st.Pending++
		switch {
		case c.st.Pending >= c.max:
			sizes = append(sizes, c.max)
			c.st.Pending = 0
		case c.st.Pending >= c.min && now.Sub(c.st.Open) >= c.window:
			sizes = append(sizes, c.st.Pending)
			c.st.Pending = 0
		}
	}
	return sizes
}

// Flush closes the open chunk, if any, and reports its size.
func (c *Chunker) Flush() (int, bool) {
	if c.st.Pending == 0 {
		return 0, false
	}
	n := c.st.Pending
	c.st.Pending = 0
	return n, true
}

// Export snapshots the chunker state for checkpointing.
func (c *Chunker) Export() State { return c.st }

// Import restores a previously exported state.
func (c *Chunker) Import(st State) { c.st = st }
