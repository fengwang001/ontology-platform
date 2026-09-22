package reasm

import "time"

// manualClock is a test clock advanced explicitly by the test.
type manualClock struct{ t time.Time }

func newManualClock() *manualClock {
	return &manualClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func (c *manualClock) now() time.Time { return c.t }

func (c *manualClock) advance(d time.Duration) { c.t = c.t.Add(d) }
