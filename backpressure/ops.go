package backpressure

// Add increases the occupancy by n and reports whether the increment
// was accepted.
//
// A non-positive n is invalid: it returns false and changes nothing.
// If level+n would exceed the hard limit the whole increment is
// rejected: the level is unchanged, Rejected grows by n, and false is
// returned. Reaching exactly the hard limit is accepted.
func (c *Controller) Add(n int64) bool {
	if n <= 0 {
		return false
	}
	c.mu.Lock()
	if c.level+n > c.hard {
		// Rejected increments never change the state or the
		// Pauses/Resumes counters.
		c.rejected += n
		c.mu.Unlock()
		return false
	}
	c.level += n
	var changed *State
	if c.state == Flowing && c.level >= c.high {
		c.state = Paused
		c.pauses++
		s := Paused
		changed = &s
	}
	c.dispatchLocked(changed)
	c.mu.Unlock()
	return true
}

// Sub decreases the occupancy by n. A non-positive n is invalid and
// changes nothing. The level is clamped at zero and the clamped value
// is used to decide whether to resume flowing.
func (c *Controller) Sub(n int64) {
	if n <= 0 {
		return
	}
	c.mu.Lock()
	c.level -= n
	if c.level < 0 {
		c.level = 0
	}
	var changed *State
	if c.state == Paused && c.level <= c.low {
		c.state = Flowing
		c.resumes++
		s := Flowing
		changed = &s
	}
	c.dispatchLocked(changed)
	c.mu.Unlock()
}

// dispatchLocked invokes the registered callbacks exactly once each,
// in registration order, when a real transition happened. It must be
// called with c.mu held; callbacks run after c.mu is released for
// them, so a callback may safely call Stat. dispMu is acquired while
// c.mu is still held (lock order: mu -> dispMu), which guarantees
// callbacks observe transitions in the order they occurred.
func (c *Controller) dispatchLocked(changed *State) {
	if changed == nil {
		return
	}
	cbs := make([]func(State), len(c.callbacks))
	copy(cbs, c.callbacks)
	c.dispMu.Lock()
	c.mu.Unlock()
	for _, f := range cbs {
		f(*changed)
	}
	c.dispMu.Unlock()
	c.mu.Lock()
}
