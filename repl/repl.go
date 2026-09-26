// Package repl implements a single replica: an ordered log (1-based indices),
// commit/apply cursors, and the deterministic state machine state.
// It depends only on package sm.
package repl

import (
	"errors"
	"sync"

	"ontology/sm"
)

// Sentinel errors: callers decide with errors.Is; the three are distinct.
var (
	ErrCommitOutOfRange   = errors.New("repl: commit index out of range")
	ErrEmptyCommand       = errors.New("repl: append rejected: empty or unknown command")
	ErrSnapshotOutOfRange = errors.New("repl: snapshot index out of range")
)

// Replica is one state-machine replica. The zero value is NOT ready; use New.
type Replica struct {
	mu sync.RWMutex
	// log holds entries in order; entry with 1-based index i is log[i-1].
	log []sm.Cmd
	// Invariant maintained by every method: 0 <= lastApplied <= committed <= len(log).
	committed   int
	lastApplied int
	state       int
	// reads counts log entries read during the most recent Apply call.
	// Unexported on purpose: it must never appear in the public API.
	reads int
}

// New returns an empty replica with state and all cursors at 0.
func New() *Replica {
	return &Replica{}
}

// Append appends one command to the log tail.
// An unknown/nil command is rejected without touching any state.
func (r *Replica) Append(c sm.Cmd) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !c.Valid() {
		return ErrEmptyCommand
	}
	r.log = append(r.log, c)
	return nil
}

// Commit advances the commit cursor to max(committed, i); it cannot pass the
// log tail. An out-of-range i is rejected without touching any state.
func (r *Replica) Commit(i int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if i < 0 || i > len(r.log) {
		return ErrCommitOutOfRange
	}
	if i > r.committed {
		r.committed = i
	}
	return nil
}

// Apply applies entries lastApplied+1 .. committed in ascending index order,
// resuming from lastApplied (only newly committed entries are ever read).
func (r *Replica) Apply() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reads = 0
	for r.lastApplied < r.committed {
		r.state = sm.Apply(r.log[r.lastApplied], r.state) // entry index lastApplied+1
		r.lastApplied++
		r.reads++
	}
}

// Restart restores from a snapshot: state becomes snapState and the apply
// cursor becomes snapIndex. The log is kept and committed is unchanged.
// snapIndex must satisfy 0 <= snapIndex <= committed, else it is rejected
// without touching any state.
func (r *Replica) Restart(snapIndex, snapState int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if snapIndex < 0 || snapIndex > r.committed {
		return ErrSnapshotOutOfRange
	}
	r.state = snapState
	r.lastApplied = snapIndex
	return nil
}

// State returns the current accumulator value.
func (r *Replica) State() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.state
}

// LastApplied returns the highest index applied to the state machine.
func (r *Replica) LastApplied() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.lastApplied
}

// Committed returns the highest committed log index.
func (r *Replica) Committed() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.committed
}

// LastApplyReadOne reports whether the most recent Apply read exactly one log
// entry. It exposes a verdict only, never the counter's numeric value.
func (r *Replica) LastApplyReadOne() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.reads == 1
}

// LogLen returns the number of appended log entries.
func (r *Replica) LogLen() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.log)
}
