// Package replica implements the local replicated log of a single replica.
package replica

import (
	"errors"
	"fmt"
	"sync"
)

// Entry is a single replicated log record.
type Entry struct {
	Index uint64
	Term  uint64
	Data  string
}

// ErrTermRegression is returned when an entry term is lower than the
// term of the latest persisted entry on the replica.
var ErrTermRegression = errors.New("replica: term regression")

// Replica is the local in-memory log of one replica.
// It is safe for concurrent use.
type Replica struct {
	id      int
	mu      sync.Mutex
	entries []Entry // entries[k] holds Index k+1; indexes are contiguous
}

// New creates an empty replica with the given id.
func New(id int) *Replica {
	return &Replica{id: id}
}

// Append persists e. e.Index must be exactly Match()+1 and e.Term must
// not be lower than the term of the latest persisted entry.
func (r *Replica) Append(e Entry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	want := uint64(len(r.entries)) + 1
	if e.Index != want {
		return fmt.Errorf("replica: non-contiguous index %d, want %d", e.Index, want)
	}
	if n := len(r.entries); n > 0 && e.Term < r.entries[n-1].Term {
		return fmt.Errorf("%w: term %d after term %d", ErrTermRegression, e.Term, r.entries[n-1].Term)
	}
	r.entries = append(r.entries, e)
	return nil
}

// Match returns the highest persisted index (0 when the log is empty).
func (r *Replica) Match() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return uint64(len(r.entries))
}

// Get returns the entry at index i, or false if it is not persisted.
func (r *Replica) Get(i uint64) (Entry, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if i == 0 || i > uint64(len(r.entries)) {
		return Entry{}, false
	}
	return r.entries[i-1], true
}

// Truncate discards every entry with Index >= from. It is used to roll
// back a conflicting tail before re-replication.
func (r *Replica) Truncate(from uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if from < 1 {
		from = 1
	}
	if from <= uint64(len(r.entries)) {
		r.entries = r.entries[:from-1]
	}
}
