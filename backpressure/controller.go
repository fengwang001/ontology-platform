package backpressure

import "sync"

// Controller is a watermark controller with hysteresis.
//
// The zero value is not usable; construct one with New.
// All methods are safe for concurrent use.
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
	observers []func(State)
}

// New creates a Controller. It requires low < high <= hard and all
// three values non-negative; otherwise it returns ErrBadWatermark
// and a nil controller.
func New(low, high, hard int64) (*Controller, error) {
	if low < 0 || high < 0 || hard < 0 {
		return nil, ErrBadWatermark
	}
	if low >= high || high > hard {
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
// Reaching exactly the hard limit is accepted.
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
	var changed bool
	if c.state == Flowing && c.level >= c.high {
		c.state = Paused
		c.pauses++
		changed = true
	}
	observers := c.snapshotObserversLocked()
	c.mu.Unlock()
	if changed {
		notify(observers, Paused)
	}
	return true
}

// Sub decreases the occupancy by n.
//
// A non-positive n is invalid and changes nothing. The level is
// clamped at zero; the clamped value decides whether flow resumes.
func (c *Controller) Sub(n int64) {
	if n <= 0 {
		return
	}
	c.mu.Lock()
	c.level -= n
	if c.level < 0 {
		c.level = 0
	}
	var changed bool
	if c.state == Paused && c.level <= c.low {
		c.state = Flowing
		c.resumes++
		changed = true
	}
	observers := c.snapshotObserversLocked()
	c.mu.Unlock()
	if changed {
		notify(observers, Flowing)
	}
}

// OnChange registers f to be called on every real state transition.
// Multiple observers may be registered; they run in registration
// order, outside the controller lock, so f may safely call Stat.
func (c *Controller) OnChange(f func(State)) {
	c.mu.Lock()
	c.observers = append(c.observers, f)
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

// snapshotObserversLocked returns a copy of the observer slice.
// The caller must hold c.mu.
func (c *Controller) snapshotObserversLocked() []func(State) {
	if len(c.observers) == 0 {
		return nil
	}
	out := make([]func(State), len(c.observers))
	copy(out, c.observers)
	return out
}

// notify calls each observer once, in registration order.
func notify(observers []func(State), s State) {
	for _, f := range observers {
		f(s)
	}
}
