package demand

import "fmt"

// Maintenance operations. They never trigger an evaluation but take
// effect immediately for the next one. Error order for all of them:
// invalid parameter > load not found > state not allowed. Rejected
// operations change no state.

// AddLoad registers a new controllable load. The load starts connected
// with its min-on timer starting at the controller's current time.
func (c *Controller) AddLoad(spec LoadSpec) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reg.add(spec, c.nowLocked())
}

// RemoveLoad deletes a load. Per the specification, deleting a load that
// is connected or disconnected is rejected; since an existing load is
// always in one of those states, deletion of an existing load always
// fails with ErrStateNotAllowed.
func (c *Controller) RemoveLoad(id int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if id <= 0 {
		return fmt.Errorf("%w: load ID must be positive, got %d", ErrInvalidParam, id)
	}
	l, ok := c.reg.get(id)
	if !ok {
		return fmt.Errorf("%w: load %d", ErrLoadNotFound, id)
	}
	state := "disconnected"
	if l.connected {
		state = "connected"
	}
	return fmt.Errorf("%w: cannot delete load %d while %s", ErrStateNotAllowed, id, state)
}

// LockLoad locks a load to stay connected: while locked it is excluded
// from cutting. Locking a disconnected load does not restore it.
func (c *Controller) LockLoad(id int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	l, err := c.loadForOp(id)
	if err != nil {
		return err
	}
	if l.locked {
		return fmt.Errorf("%w: load %d already locked", ErrStateNotAllowed, id)
	}
	l.locked = true
	return nil
}

// UnlockLoad removes the operator lock; the load participates in
// cutting again from the next evaluation on.
func (c *Controller) UnlockLoad(id int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	l, err := c.loadForOp(id)
	if err != nil {
		return err
	}
	if !l.locked {
		return fmt.Errorf("%w: load %d is not locked", ErrStateNotAllowed, id)
	}
	l.locked = false
	return nil
}

func (c *Controller) loadForOp(id int) (*loadState, error) {
	if id <= 0 {
		return nil, fmt.Errorf("%w: load ID must be positive, got %d", ErrInvalidParam, id)
	}
	l, ok := c.reg.get(id)
	if !ok {
		return nil, fmt.Errorf("%w: load %d", ErrLoadNotFound, id)
	}
	return l, nil
}

// PeakDemand returns the highest measured window demand recorded so far
// (ties: earliest window end), and false if no window has closed yet.
func (c *Controller) PeakDemand() (PeakRecord, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.peak.get()
}
