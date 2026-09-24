// Package visible decides whether a version written by a transaction is
// visible to a read snapshot, in a constant number of lookups.
package visible

import (
	"errors"

	"ontology/snapshot"
	"ontology/txn"
)

// ErrCorruptChain reports a committed entry whose sequence exceeds the
// last sequence the table ever handed out: the commit chain went
// backwards somewhere and is corrupt.
var ErrCorruptChain = errors.New("visible: non-increasing commit sequence")

// Checker decides visibility. It is not safe for concurrent use; give
// each goroutine its own Checker so the per-check lookup counters never
// interfere.
type Checker struct {
	lookups int
}

// Lookups returns how many table/set lookups the last Check performed.
func (c *Checker) Lookups() int { return c.lookups }

// Check reports whether the version written by transaction id is visible
// to snap: the writer must be committed, its commit sequence must be
// below the snapshot high-water mark, and it must not be in the
// snapshot's active set.
func (c *Checker) Check(s *snapshot.Snapshot, id txn.ID) (bool, error) {
	c.lookups = 0
	if s.Released() {
		return false, snapshot.ErrSnapshotReleased
	}
	c.lookups++
	e, err := s.Table().Status(id)
	if err != nil {
		return false, err
	}
	if e.State == txn.Aborted {
		return false, nil
	}
	if id == s.Self() {
		return true, nil // a transaction always sees its own writes
	}
	if e.State != txn.Committed {
		return false, nil
	}
	if e.CommitSeq > s.Table().LastSeq() {
		return false, ErrCorruptChain
	}
	if e.CommitSeq >= s.HiWater() {
		return false, nil
	}
	c.lookups++
	if s.IsActive(id) {
		return false, nil
	}
	return true, nil
}

// NaiveCheck is the reference implementation for tests and the demo: it
// finds the transaction by scanning the whole table, then applies the
// same rule. It must agree with Check on every input.
func NaiveCheck(s *snapshot.Snapshot, id txn.ID) (bool, error) {
	if s.Released() {
		return false, snapshot.ErrSnapshotReleased
	}
	var e txn.Entry
	found := false
	s.Table().Range(func(tid txn.ID, te txn.Entry) bool {
		if tid == id {
			e, found = te, true
		}
		return !found
	})
	if !found {
		return false, txn.ErrUnknownTxn
	}
	if e.State == txn.Aborted {
		return false, nil
	}
	if id == s.Self() {
		return true, nil
	}
	if e.State != txn.Committed || e.CommitSeq >= s.HiWater() {
		return false, nil
	}
	return !s.IsActive(id), nil
}
