// Package visible decides whether the version written by a transaction is
// visible to a given read snapshot.
//
// Rule: visible iff the writer is committed, its commit sequence is below
// the snapshot watermark, and it is not in the snapshot's active set.
// The snapshot owner always sees its own writes; aborted writers never
// become visible.
package visible

import (
	"ontology/snapshot"
	"ontology/txn"
)

// Visible reports whether the version written by id is visible to s.
func Visible(t *txn.Table, s *snapshot.Snapshot, id txn.ID) (bool, error) {
	vis, _, err := check(t, s, id)
	return vis, err
}

// Counted is Visible plus the number of lookups this call performed.
func Counted(t *txn.Table, s *snapshot.Snapshot, id txn.ID) (vis bool, lookups int, err error) {
	return check(t, s, id)
}

// check does the work. The lookups counter is a per-call local, never
// shared, so concurrent checks cannot interfere with each other.
func check(t *txn.Table, s *snapshot.Snapshot, id txn.ID) (vis bool, lookups int, err error) {
	if s.Released() {
		return false, 0, snapshot.ErrReleased
	}
	if id == s.Owner() {
		return true, 0, nil // a transaction always sees its own writes
	}
	rec, err := t.Get(id) // lookup 1: transaction table
	lookups++
	if err != nil {
		return false, lookups, err
	}
	if rec.Status != txn.Committed {
		return false, lookups, nil // still active or aborted: invisible
	}
	lookups++ // lookup 2: snapshot active-set membership
	return rec.CommitSeq < s.Watermark() && !s.Active(id), lookups, nil
}

// Naive is the reference implementation: it scans the whole transaction
// table instead of doing a direct lookup. It exists for the tests and the
// demo only; production decisions must use Visible.
func Naive(t *txn.Table, s *snapshot.Snapshot, id txn.ID) (bool, error) {
	if s.Released() {
		return false, snapshot.ErrReleased
	}
	if id == s.Owner() {
		return true, nil
	}
	rec, found := txn.Record{}, false
	t.Each(func(rid txn.ID, r txn.Record) {
		if rid == id {
			rec, found = r, true
		}
	})
	if !found {
		return false, txn.ErrUnknown
	}
	return rec.Status == txn.Committed && rec.CommitSeq < s.Watermark() && !s.Active(id), nil
}
