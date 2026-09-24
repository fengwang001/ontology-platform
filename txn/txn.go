// Package txn tracks transaction IDs and their commit status in memory.
package txn

import (
	"errors"
	"sync"
)

var (
	// ErrUnknown is returned when a transaction ID was never begun.
	ErrUnknown = errors.New("txn: unknown transaction id")
	// ErrNonMonotonic is returned when a commit sequence number does not
	// strictly increase along the commit chain (corruption).
	ErrNonMonotonic = errors.New("txn: commit sequence not increasing")
)

// ID identifies a transaction.
type ID uint64

// Status is the commit state of a transaction.
type Status int

const (
	Active Status = iota
	Committed
	Aborted
)

// Record is the stored state of one transaction.
type Record struct {
	Status    Status
	CommitSeq uint64 // valid only when Status == Committed
}

// Table is a goroutine-safe in-memory transaction table.
type Table struct {
	mu      sync.RWMutex
	recs    map[ID]Record
	lastSeq uint64
}

func NewTable() *Table { return &Table{recs: make(map[ID]Record)} }

// Begin registers a new active transaction.
func (t *Table) Begin(id ID) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.recs[id] = Record{Status: Active}
}

// Commit marks id committed with the given commit sequence number.
func (t *Table) Commit(id ID, seq uint64) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.recs[id]; !ok {
		return ErrUnknown
	}
	if seq <= t.lastSeq {
		return ErrNonMonotonic
	}
	t.lastSeq = seq
	t.recs[id] = Record{Status: Committed, CommitSeq: seq}
	return nil
}

// Abort marks id aborted; an aborted transaction is never visible.
func (t *Table) Abort(id ID) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.recs[id]; !ok {
		return ErrUnknown
	}
	t.recs[id] = Record{Status: Aborted}
	return nil
}

// Get returns the record for id in one map lookup.
func (t *Table) Get(id ID) (Record, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	rec, ok := t.recs[id]
	if !ok {
		return Record{}, ErrUnknown
	}
	return rec, nil
}

// Each iterates the whole table; it exists for the naive reference
// implementation only, never for the real visibility check.
func (t *Table) Each(fn func(ID, Record)) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	for id, rec := range t.recs {
		fn(id, rec)
	}
}
