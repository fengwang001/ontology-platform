package payledger

import "fmt"

// clock tracks the day of the last accepted operation. Accepted operations
// move it forward; rejected operations and queries never do.
type clock struct {
	last    int64
	started bool
}

// check reports ErrClockRegression when now is earlier than the last
// accepted operation's day. It never mutates the clock.
func (c *clock) check(now int64) error {
	if c.started && now < c.last {
		return fmt.Errorf("%w: now=%d last=%d", ErrClockRegression, now, c.last)
	}
	return nil
}

// advance records the day of a freshly accepted operation.
func (c *clock) advance(now int64) {
	c.last = now
	c.started = true
}
