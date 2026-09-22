package correlate

// Counters returns a point-in-time snapshot of every outcome counter after
// applying lazy timeouts at the injected current time. The counter values
// together with InFlight are self-consistent: every issued request ends in
// exactly one of Completed, TimedOut or Canceled, and every failed delivery
// increments exactly one of the three orphan counters.
func (c *Correlator) Counters() Counter {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sweep(c.now())
	return c.c
}

// Snapshot reports the in-flight count and every counter in one read, so the
// returned values describe the same instant of the injected clock.
type Snapshot struct {
	InFlight int
	Counter
}

// Snapshot reads InFlight and Counters atomically.
func (c *Correlator) SnapshotState() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sweep(c.now())
	return Snapshot{InFlight: c.pool.Size(), Counter: c.c}
}

// Finished reports how many requests have reached a terminal state.
func (c *Counter) Finished() uint64 {
	return c.Completed + c.TimedOut + c.Canceled
}

// Orphans reports the total number of rejected replies across all three
// orphan classes.
func (c *Counter) Orphans() uint64 {
	return c.OrphanUnknown + c.OrphanIdle + c.OrphanStale
}
