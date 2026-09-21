// Package replica implements the local log of a single replica.
package replica

import "fmt"

// Entry is a single log record.
type Entry struct {
	Index uint64
	Term  uint64
	Data  string
}

// Replica is the local, contiguous log of one replica.
type Replica struct {
	id      int
	entries []Entry // entries[i-1] holds Index i
}

// New creates an empty replica with the given id.
func New(id int) *Replica {
	return &Replica{id: id}
}

// Append persists e. Index must be Match()+1 and Term must not regress.
func (r *Replica) Append(e Entry) error {
	if want := r.Match() + 1; e.Index != want {
		return fmt.Errorf("replica %d: append index %d, want %d", r.id, e.Index, want)
	}
	if last, ok := r.Get(r.Match()); ok && e.Term < last.Term {
		return fmt.Errorf("replica %d: term regressed from %d to %d", r.id, last.Term, e.Term)
	}
	r.entries = append(r.entries, e)
	return nil
}

// Match returns the highest persisted Index.
func (r *Replica) Match() uint64 {
	return uint64(len(r.entries))
}

// Get returns the entry at Index i.
func (r *Replica) Get(i uint64) (Entry, bool) {
	if i == 0 || i > uint64(len(r.entries)) {
		return Entry{}, false
	}
	return r.entries[i-1], true
}

// Truncate drops all entries with Index >= from.
func (r *Replica) Truncate(from uint64) {
	if from == 0 {
		from = 1
	}
	if from <= uint64(len(r.entries)) {
		r.entries = r.entries[:from-1]
	}
}
