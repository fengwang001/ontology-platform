// Package record defines a bitemporal record: a key/value pair stamped
// with both a valid-time and a transaction-time interval.
package record

import "ontology/interval"

// Record is one version of a key. Valid describes when the fact held in
// the real world; Tx describes when the system believed it.
type Record struct {
	Key   string
	Value int64
	Valid interval.Interval
	Tx    interval.Interval
}

// New validates both intervals and returns the record. Empty intervals
// are rejected with interval.ErrEmpty.
func New(key string, value int64, valid, tx interval.Interval) (Record, error) {
	if valid.Empty() || tx.Empty() {
		return Record{}, interval.ErrEmpty
	}
	return Record{Key: key, Value: value, Valid: valid, Tx: tx}, nil
}

// Covers reports whether the record answers the point query
// (validAt, txAt).
func (r Record) Covers(validAt, txAt int64) bool {
	return r.Valid.Contains(validAt) && r.Tx.Contains(txAt)
}
