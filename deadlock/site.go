package deadlock

// waitEntry is a queued lock request.
type waitEntry struct {
	txn  string
	mode Mode
}

// lockState is the per-lock table at one site: current holders plus the
// FIFO request queue.
type lockState struct {
	holders map[string]Mode
	queue   []waitEntry
}

func newLockState() *lockState {
	return &lockState{holders: make(map[string]Mode)}
}

// holdersCompatible reports whether mode is compatible with every current
// holder of the lock.
func (l *lockState) holdersCompatible(mode Mode) bool {
	for _, m := range l.holders {
		if !compatible(m, mode) {
			return false
		}
	}
	return true
}

// Site owns a set of locks and the wait-for edges of the transactions
// currently blocked on those locks. A site only ever sees local state.
type Site struct {
	name  string
	locks map[string]*lockState
	// waits[txn] is the set of transactions txn is waiting for at this
	// site (incompatible holders plus incompatible waiters ahead of it
	// in the queue). Present only while txn is blocked here.
	waits map[string]map[string]bool
}

func newSite(name string) *Site {
	return &Site{
		name:  name,
		locks: make(map[string]*lockState),
		waits: make(map[string]map[string]bool),
	}
}

// waitSet computes the wait-for edges created when txn enqueues on lock l
// with the given mode: all incompatible holders and all incompatible
// waiters already ahead of it in the queue.
func waitSetFor(l *lockState, txn string, mode Mode) map[string]bool {
	waits := make(map[string]bool)
	for holder, m := range l.holders {
		if !compatible(m, mode) {
			waits[holder] = true
		}
	}
	for _, e := range l.queue {
		if e.txn != txn && !compatible(e.mode, mode) {
			waits[e.txn] = true
		}
	}
	return waits
}
