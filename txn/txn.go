// Package txn defines transaction IDs and their commit status records.
package txn

import "errors"

// ID identifies a transaction.
type ID uint64

// Status is the commit state of a transaction.
type Status uint8

const (
	Active Status = iota
	Committed
	Aborted
)

// ErrUnknown is returned when a transaction ID is not in the table.
var ErrUnknown = errors.New("txn: unknown transaction id")

// Record is the committed state of one transaction.
// Commit is the commit number, meaningful only when Status == Committed.
type Record struct {
	Status Status
	Commit uint64
}

// Table is an in-memory transaction status table. It is built once and
// only read afterwards, so it needs no locking.
type Table struct {
	recs map[ID]Record
}

// NewTable builds a Table from the given records.
func NewTable(recs map[ID]Record) *Table { return &Table{recs: recs} }

// Get returns the record for id, or ErrUnknown if id is not present.
func (t *Table) Get(id ID) (Record, error) {
	r, ok := t.recs[id]
	if !ok {
		return Record{}, ErrUnknown
	}
	return r, nil
}

// Len returns the number of transactions in the table.
func (t *Table) Len() int { return len(t.recs) }
