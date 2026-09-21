package backpressure

import "sync"

// Controller is a watermark controller with hysteresis. It is safe for
// concurrent use.
type Controller struct {
	mu        sync.Mutex
	low       int64
	high      int64
	hard      int64
	level     int64
	state     State
	pauses    int
	resumes   int
	rejected  int64
	listeners []func(State)
}

// New creates a Controller with the given watermarks. It requires
// 0 <= low < high <= hard; otherwise it returns ErrBadWatermark and a
// nil controller. A low watermark of 0 is valid.
func New(low, high, hard int64) (*Controller, error) {
	if low < 0 || high < 0 || hard < 0 || low >= high || high > hard {
		return nil, ErrBadWatermark
	}
	return &Controller{
		low:   low,
		high:  high,
		hard:  hard,
		state: Flowing,
	}, nil
}

// Add increases the occupancy by n and reports whether it was accepted.
//
// A non-positive n is invalid: it returns false and changes nothing.
// If level+n would exceed the hard limit, the whole amount is rejected:
// the level is unchanged, Rejected grows by n, and false is returned.
// An addition landing exactly on the hard limit is accepted.
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
	callbacks := c.evaluateLocked()
	c.mu.Unlock()
	notify(callbacks)
	return true
}

// Sub decreases the occupancy by n. A non-positive n is invalid and does
// nothing. The level is clamped at zero and never goes negative; state
// evaluation uses the clamped value.
func (c *Controller) Sub(n int64) {
	if n <= 0 {
		return
	}
	c.mu.Lock()
	c.level -= n
	if c.level < 0 {
		c.level = 0
	}
	callbacks := c.evaluateLocked()
	c.mu.Unlock()
	notify(callbacks)
}

// OnChange registers f to be called on every real state transition.
// Multiple listeners may be registered; they are invoked in registration
// order. Listeners are called without the internal lock held, so f may
// safely call Stat (or any other method) on the Controller.
func (c *Controller) OnChange(f func(State)) {
	if f == nil {
		return
	}
	c.mu.Lock()
	c.listeners = append(c.listeners, f)
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

// evaluateLocked applies the hysteresis rule to the current level and, on
// a real transition, updates the counters and returns the pending
// notifications. The caller must hold c.mu.
func (c *Controller) evaluateLocked() []pendingCall {
	var next State
	switch c.state {
	case Flowing:
		if c.level < c.high {
			return nil
		}
		next = Paused
		c.pauses++
	case Paused:
		if c.level > c.low {
			return nil
		}
		next = Flowing
		c.resumes++
	default:
		return nil
	}
	c.state = next
	callbacks := make([]pendingCall, 0, len(c.listeners))
	for _, f := range c.listeners {
		callbacks = append(callbacks, pendingCall{f: f, state: next})
	}
	return callbacks
}

// pendingCall is a listener invocation deferred until the lock is released.
type pendingCall struct {
	f     func(State)
	state State
}

// notify invokes pending listener calls in registration order.
func notify(callbacks []pendingCall) {
	for _, p := range callbacks {
		p.f(p.state)
	}
}
