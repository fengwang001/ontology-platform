// Package txn tracks transaction IDs and their commit status in memory.
package txn

import (
	"errors"
	"fmt"
	"sync"
)

// ID identifies a transaction.
type ID uint64

// Status is the commit state of a transaction.
type Status int

const (
	InProgress Status = iota
	Committed
	Aborted
)

// Record describes a transaction's commit state.
type Record struct {
	Status    Status
	CommitSeq uint64 // valid only when Status == Committed
}

// ErrUnknownTxn is returned for an ID that is not in the table.
var ErrUnknownTxn = errors.New("txn: unknown transaction id")

// Table tracks commit states of all transactions. Safe for concurrent use.
type Table struct {
	mu   sync.RWMutex
	next ID
	recs map[ID]Record
}

func NewTable() *Table { return &Table{recs: make(map[ID]Record)} }

// Begin allocates a new in-progress transaction.
func (t *Table) Begin() ID {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.next++
	t.recs[t.next] = Record{Status: InProgress}
	return t.next
}

// Commit marks id committed with the given commit sequence number.
func (t *Table) Commit(id ID, seq uint64) error {
	return t.set(id, Record{Status: Committed, CommitSeq: seq})
}

// Abort marks id aborted; aborted versions are never visible.
func (t *Table) Abort(id ID) error {
	return t.set(id, Record{Status: Aborted})
}

func (t *Table) set(id ID, rec Record) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.recs[id]; !ok {
		return fmt.Errorf("%w: %d", ErrUnknownTxn, id)
	}
	t.recs[id] = rec
	return nil
}

// Get returns the record for id, or ErrUnknownTxn.
func (t *Table) Get(id ID) (Record, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	rec, ok := t.recs[id]
	if !ok {
		return Record{}, fmt.Errorf("%w: %d", ErrUnknownTxn, id)
	}
	return rec, nil
}

// Each calls fn for every record; used by the naive reference only.
func (t *Table) Each(fn func(ID, Record)) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	for id, rec := range t.recs {
		fn(id, rec)
	}
}
