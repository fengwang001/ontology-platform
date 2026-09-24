// Package visible decides whether a version is visible to a read snapshot.
//
// Rule: a version written by transaction T is visible to snapshot S iff T
// committed with seq < S's high-water mark and T is not in S's active set.
// Writes by S's own transaction are visible to S; aborted writes never are.
package visible

import (
	"errors"
	"fmt"

	"ontology/snapshot"
	"ontology/txn"
)

// ErrCorruptChain reports a version chain whose commit seqs do not
// strictly decrease from newest to oldest.
var ErrCorruptChain = errors.New("visible: non-increasing commit seq in version chain")

// Judge decides visibility. Not safe for sharing; use one per goroutine.
type Judge struct {
	table   *txn.Table
	lookups int
}

func NewJudge(t *txn.Table) *Judge { return &Judge{table: t} }

// Lookups returns the lookup count of the most recent Visible call.
func (j *Judge) Lookups() int { return j.lookups }

// Visible reports whether any version in chain (newest first) is visible
// to snap. A single-version chain costs at most 2 lookups.
func (j *Judge) Visible(snap *snapshot.Snapshot, chain []txn.ID) (bool, error) {
	j.lookups = 0
	hwm, err := snap.HWM()
	if err != nil {
		return false, err
	}
	var last uint64
	seen := false
	for _, id := range chain {
		j.lookups++
		rec, err := j.table.Get(id)
		if err != nil {
			return false, err
		}
		if rec.Status == txn.Aborted {
			continue // aborted versions are never visible
		}
		if id == snap.Owner() {
			return true, nil // own writes are visible to own snapshot
		}
		if rec.Status != txn.Committed {
			continue
		}
		if seen && rec.CommitSeq >= last {
			return false, fmt.Errorf("%w: seq %d after %d", ErrCorruptChain, rec.CommitSeq, last)
		}
		seen, last = true, rec.CommitSeq
		if rec.CommitSeq >= hwm {
			continue // left-closed right-open: seq == hwm is invisible
		}
		j.lookups++
		active, err := snap.Active(id)
		if err != nil {
			return false, err
		}
		if !active {
			return true, nil
		}
	}
	return false, nil
}

// naiveReference is the test-only baseline: it scans the whole transaction
// table for every version instead of doing constant-time lookups.
func naiveReference(t *txn.Table, snap *snapshot.Snapshot, chain []txn.ID) bool {
	hwm, err := snap.HWM()
	if err != nil {
		return false
	}
	for _, id := range chain {
		var rec txn.Record
		found := false
		t.Each(func(rid txn.ID, r txn.Record) {
			if rid == id {
				rec, found = r, true
			}
		})
		if !found || rec.Status == txn.Aborted {
			continue
		}
		if id == snap.Owner() {
			return true
		}
		if rec.Status != txn.Committed || rec.CommitSeq >= hwm {
			continue
		}
		if active, err := snap.Active(id); err == nil && !active {
			return true
		}
	}
	return false
}
