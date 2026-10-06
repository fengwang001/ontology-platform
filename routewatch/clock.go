package routewatch

type clock struct {
	last int64
}

// newClock starts the logical clock at the depot departure time.
func newClock(initial int64) *clock { return &clock{last: initial} }

// accept returns ErrClockRollback when t is earlier than the last accepted
// operation; otherwise it advances the clock. A rejected operation never
// mutates the clock.
func (c *clock) accept(t int64) error {
	if t < c.last {
		return ErrClockRollback
	}
	c.last = t
	return nil
}
