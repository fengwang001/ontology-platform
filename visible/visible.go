// Package visible decides whether a version is visible to a read snapshot.
//
// Rule: a version written by transaction T is visible to snapshot S iff
// T is committed, T's commit sequence is below S's high-water mark, and
// T is not in S's active set (unless T is S's own transaction).
package visible

import (
	"errors"

	"ontology/snapshot"
	"ontology/txn"
)

// ErrCorruptChain marks a version chain whose commit sequences do not
// strictly decrease from newest to oldest.
var ErrCorruptChain = errors.New("visible: non-increasing commit sequence in version chain")

// Check reports whether version v is visible to snap. lookups counts the
// table probes of this single judgment and never exceeds 2.
func Check(t *txn.Table, snap *snapshot.Snapshot, v txn.Version) (ok bool, lookups int, err error) {
	if err := snap.Err(); err != nil {
		return false, 0, err
	}
	if v.Txn == snap.Own() {
		return true, 0, nil
	}
	seq, committed, err := t.Lookup(v.Txn)
	lookups++
	if err != nil {
		return false, lookups, err
	}
	if !committed || seq >= snap.Water() {
		return false, lookups, nil
	}
	in, err := snap.InActive(v.Txn)
	lookups++
	if err != nil {
		return false, lookups, err
	}
	return !in, lookups, nil
}

// CheckChain validates that commit sequences strictly decrease along the
// version chain from head to oldest.
func CheckChain(head *txn.Version) error {
	for v := head; v != nil && v.Prev != nil; v = v.Prev {
		if v.Prev.Seq >= v.Seq {
			return ErrCorruptChain
		}
	}
	return nil
}

// NaiveCheck is the reference implementation for tests and the demo: it
// scans the whole transaction table instead of doing O(1) lookups.
func NaiveCheck(t *txn.Table, snap *snapshot.Snapshot, v txn.Version) (bool, error) {
	if err := snap.Err(); err != nil {
		return false, err
	}
	if v.Txn == snap.Own() {
		return true, nil
	}
	var seq uint64
	found, committed := false, false
	t.Each(func(id txn.ID, s uint64, c bool) bool {
		if id == v.Txn {
			found, committed, seq = true, c, s
		}
		return !found
	})
	if !found {
		return false, txn.ErrUnknownTxn
	}
	if !committed || seq >= snap.Water() {
		return false, nil
	}
	in, err := snap.InActive(v.Txn)
	if err != nil {
		return false, err
	}
	return !in, nil
}
