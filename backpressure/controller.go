package backpressure

// Add increases the occupancy by n and reports whether it was accepted.
//
// A non-positive n is invalid: it returns false and changes nothing.
// If level+n would exceed the hard limit, the whole amount is rejected
// (never partially accepted), Rejected grows by n, and no state change
// happens. Landing exactly on the hard limit is accepted.
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
	c.mu.Unlock()
	if changed {
		c.notify(Paused)
	}
	return true
}

// Sub decreases the occupancy by n. A non-positive n is invalid and
// changes nothing. The level is clamped at 0 and never goes negative;
// the resume decision uses the clamped value.
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
	c.mu.Unlock()
	if changed {
		c.notify(Flowing)
	}
}

// OnChange registers f to be called on every real state transition.
// Multiple callbacks are invoked in registration order, outside the
// controller lock, so f may safely call Stat.
func (c *Controller) OnChange(f func(State)) {
	c.mu.Lock()
	c.callbacks = append(c.callbacks, f)
	c.mu.Unlock()
}

// Stat returns a consistent snapshot of the controller.
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

// notify invokes all registered callbacks in registration order.
// It is called without holding the lock; the callback slice is only
// appended to, so copying it under the lock keeps iteration safe.
func (c *Controller) notify(s State) {
	c.mu.Lock()
	callbacks := make([]func(State), len(c.callbacks))
	copy(callbacks, c.callbacks)
	c.mu.Unlock()
	for _, f := range callbacks {
		f(s)
	}
}
