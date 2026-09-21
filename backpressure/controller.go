package backpressure

// Add increases the occupancy by n and reports whether it was accepted.
//
// A non-positive n is invalid: it returns false and changes nothing.
// If level+n would exceed the hard limit the whole amount is rejected:
// the level is unchanged, Rejected grows by n, and false is returned.
// Exactly reaching the hard limit is accepted. When the level reaches
// or exceeds the high watermark the controller enters Paused.
func (c *Controller) Add(n int64) bool {
	if n <= 0 {
		return false
	}
	c.mu.Lock()
	if c.level+n > c.hard {
		c.rejected += n
		c.mu.Unlock()
		return false
	}
	c.level += n
	changed := false
	if c.state == Flowing && c.level >= c.high {
		c.state = Paused
		c.pauses++
		changed = true
	}
	callbacks := c.callbacks
	state := c.state
	c.mu.Unlock()
	if changed {
		notify(callbacks, state)
	}
	return true
}

// Sub decreases the occupancy by n.
//
// A non-positive n is invalid and changes nothing. The level is
// clamped at zero and never goes negative. When the level drops to
// the low watermark or below the controller resumes Flowing.
func (c *Controller) Sub(n int64) {
	if n <= 0 {
		return
	}
	c.mu.Lock()
	c.level -= n
	if c.level < 0 {
		c.level = 0
	}
	changed := false
	if c.state == Paused && c.level <= c.low {
		c.state = Flowing
		c.resumes++
		changed = true
	}
	callbacks := c.callbacks
	state := c.state
	c.mu.Unlock()
	if changed {
		notify(callbacks, state)
	}
}

// OnChange registers f to be called on every real state transition.
// Multiple callbacks may be registered; they run in registration
// order, outside the controller lock, so f may safely call Stat.
func (c *Controller) OnChange(f func(State)) {
	if f == nil {
		return
	}
	c.mu.Lock()
	c.callbacks = append(c.callbacks, f)
	c.mu.Unlock()
}

// Stat returns a snapshot of the controller's current state.
func (c *Controller) Stat() Report {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Report{
		Level:    c.level,
		State:    c.state,
		Pauses:   c.pauses,
		Resumes:  c.resumes,
		Rejected: c.rejected,
	}
}

// notify invokes each callback in registration order.
func notify(callbacks []func(State), s State) {
	for _, f := range callbacks {
		f(s)
	}
}
