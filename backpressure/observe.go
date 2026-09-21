package backpressure

// OnChange registers f to be called on every real state transition.
// Multiple callbacks may be registered; they are invoked in
// registration order. Callbacks run without the controller lock held,
// so calling Stat (or any other method) from inside a callback is
// safe and will not deadlock.
func (c *Controller) OnChange(f func(State)) {
	c.mu.Lock()
	c.callbacks = append(c.callbacks, f)
	c.mu.Unlock()
}

// Stat returns a consistent snapshot of the controller.
func (c *Controller) Stat() Report {
	c.mu.Lock()
	r := Report{
		Level:    c.level,
		State:    c.state,
		Pauses:   c.pauses,
		Resumes:  c.resumes,
		Rejected: c.rejected,
	}
	c.mu.Unlock()
	return r
}
