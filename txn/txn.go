// Package txn tracks transaction IDs and their commit status in memory.
package txn

import (
	"errors"
	"sync"
)

// ID identifies a transaction.
type ID uint64

// State is the commit status of a transaction.
type State uint8

const (
	InProgress State = iota
	Committed
	Aborted
)

// ErrUnknownTxn is returned when a transaction ID is not in the table.
var ErrUnknownTxn = errors.New("txn: unknown transaction id")

// Entry is the recorded status of one transaction.
type Entry struct {
	State     State
	CommitSeq uint64 // valid only when State == Committed
}

// Table is a concurrency-safe in-memory transaction status table.
type Table struct {
	mu      sync.RWMutex
	entries map[ID]Entry
	lastSeq uint64 // commit sequence most recently handed out
}

func NewTable() *Table { return &Table{entries: make(map[ID]Entry)} }

// Begin registers a new in-progress transaction.
func (t *Table) Begin(id ID) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.entries[id] = Entry{State: InProgress}
}

// Commit marks id committed with the given sequence number. The caller is
// the commit authority and must hand out increasing sequences; a caller
// that does not creates a corrupt, non-increasing chain that readers
// detect via LastSeq.
func (t *Table) Commit(id ID, seq uint64) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.entries[id]; !ok {
		return ErrUnknownTxn
	}
	t.entries[id] = Entry{State: Committed, CommitSeq: seq}
	t.lastSeq = seq
	return nil
}

// Abort marks id aborted; its versions are never visible.
func (t *Table) Abort(id ID) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.entries[id]; !ok {
		return ErrUnknownTxn
	}
	t.entries[id] = Entry{State: Aborted}
	return nil
}

// Status returns the recorded entry for id.
func (t *Table) Status(id ID) (Entry, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	e, ok := t.entries[id]
	if !ok {
		return Entry{}, ErrUnknownTxn
	}
	return e, nil
}

// LastSeq returns the most recently handed-out commit sequence.
func (t *Table) LastSeq() uint64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.lastSeq
}

// Range iterates the table; fn may return false to stop. Used only by the
// naive reference implementation.
func (t *Table) Range(fn func(ID, Entry) bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	for id, e := range t.entries {
		if !fn(id, e) {
			return
		}
	}
}
