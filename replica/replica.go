// Package replica implements a single replica's local, contiguous log.
package replica

import (
	"fmt"
	"sync"
)

// Entry is a single replicated log record. Index starts at 1.
type Entry struct {
	Index uint64
	Term  uint64
	Data  string
}

// Replica is one replica's local log. It is safe for concurrent use.
type Replica struct {
	mu      sync.Mutex
	id      int
	entries []Entry
}

// New returns an empty Replica with the given id.
func New(id int) *Replica {
	return &Replica{id: id}
}

// ID returns the replica id.
func (r *Replica) ID() int {
	return r.id
}

// Append persists e. The index must be exactly Match()+1 and the term
// must not regress relative to the last persisted entry.
func (r *Replica) Append(e Entry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	want := uint64(len(r.entries)) + 1
	if e.Index != want {
		return fmt.Errorf("replica %d: index %d not contiguous, want %d", r.id, e.Index, want)
	}
	if n := len(r.entries); n > 0 && e.Term < r.entries[n-1].Term {
		return fmt.Errorf("replica %d: term %d regresses below %d", r.id, e.Term, r.entries[n-1].Term)
	}
	r.entries = append(r.entries, e)
	return nil
}

// Match returns the highest persisted index (0 when empty).
func (r *Replica) Match() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return uint64(len(r.entries))
}

// Get returns the entry at index i.
func (r *Replica) Get(i uint64) (Entry, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if i == 0 || i > uint64(len(r.entries)) {
		return Entry{}, false
	}
	return r.entries[i-1], true
}

// Truncate drops all entries with Index >= from, used for conflict
// rollback. Truncating beyond the current tail is a no-op.
func (r *Replica) Truncate(from uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if from == 0 || from > uint64(len(r.entries)) {
		return
	}
	r.entries = r.entries[:from-1]
}
