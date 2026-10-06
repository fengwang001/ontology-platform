package delivery

import "sync"

// clock tracks the globally monotonic maximum accepted timestamp.
// It is the single place that rejects clock regression.
type clock struct {
	mu   sync.Mutex
	maxT int
}

// check returns ErrClockBack when t is earlier than the accepted maximum.
func (c *clock) check(t int) error {
	if t < c.maxT {
		return newError(ErrClockBack, "clock regression: timestamp earlier than accepted maximum")
	}
	return nil
}

// advance records t as the newest accepted timestamp (t must be >= maxT).
func (c *clock) advance(t int) {
	if t > c.maxT {
		c.maxT = t
	}
}
