package stats

// snapshot is an immutable point-in-time copy of an accumulator's state.
// Statistical and merge computations work on snapshots, so they can run
// outside the lock and never observe a half-applied update.
type snapshot struct {
	n       uint64
	mean    float64
	m2      float64
	skipped uint64
	invalid bool
}

// lockState runs fn while holding the accumulator lock. Every state
// transition goes through here, which is what makes concurrent Add calls
// lossless and keeps count/mean/m2 atomically consistent to readers.
func (a *Accumulator) lockState(fn func()) {
	a.mu.Lock()
	defer a.mu.Unlock()
	fn()
}

// state copies the current state under the lock and returns it.
func (a *Accumulator) state() snapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	return snapshot{
		n:       a.n,
		mean:    a.mean,
		skipped: a.skipped,
		invalid: a.invalid,
		m2:      a.m2,
	}
}
