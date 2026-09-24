// Package txn tracks transaction ids and their commit state.
package txn

import "errors"

// ErrUnknownTxn is returned when a transaction id is not in the table.
var ErrUnknownTxn = errors.New("txn: unknown transaction id")

// ID identifies a transaction.
type ID uint64

// Version is one row version written by a transaction; Prev links to the
// next older version of the same row.
type Version struct {
	Txn  ID
	Seq  uint64
	Prev *Version
}

type entry struct {
	seq       uint64
	committed bool
}

// Table records the commit state of every transaction.
type Table struct {
	m map[ID]entry
}

// NewTable returns an empty transaction table.
func NewTable() *Table { return &Table{m: make(map[ID]entry)} }

// Commit records that id committed with commit sequence seq.
func (t *Table) Commit(id ID, seq uint64) { t.m[id] = entry{seq: seq, committed: true} }

// Abort records that id rolled back; its versions are never visible.
func (t *Table) Abort(id ID) { t.m[id] = entry{} }

// Lookup returns the commit sequence and status of id in one probe.
func (t *Table) Lookup(id ID) (seq uint64, committed bool, err error) {
	e, ok := t.m[id]
	if !ok {
		return 0, false, ErrUnknownTxn
	}
	return e.seq, e.committed, nil
}

// Len returns the number of transactions in the table.
func (t *Table) Len() int { return len(t.m) }

// Each calls fn for every transaction until fn returns false.
func (t *Table) Each(fn func(id ID, seq uint64, committed bool) bool) {
	for id, e := range t.m {
		if !fn(id, e.seq, e.committed) {
			return
		}
	}
}
