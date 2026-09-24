// Package visible decides whether a version is visible to a read snapshot.
package visible

import (
	"errors"

	"ontology/snapshot"
	"ontology/txn"
)

// ErrCorrupt marks a version chain whose commit numbers are not
// strictly increasing from oldest to newest.
var ErrCorrupt = errors.New("visible: non-increasing commit numbers in version chain")

// counter records the lookups of one judgement; it is unexported and
// local to each call, so concurrent judgements never share one.
type counter struct{ n int }

// Judge returns the newest version in chain visible to s.
// chain is ordered oldest to newest.
func Judge(t *txn.Table, s *snapshot.Snapshot, chain []txn.ID) (txn.ID, bool, error) {
	id, ok, _, err := judge(t, s, chain)
	return id, ok, err
}

// JudgeCount is Judge plus the number of lookups the judgement used.
func JudgeCount(t *txn.Table, s *snapshot.Snapshot, chain []txn.ID) (txn.ID, bool, int, error) {
	return judge(t, s, chain)
}

func judge(t *txn.Table, s *snapshot.Snapshot, chain []txn.ID) (txn.ID, bool, int, error) {
	if s.Released() {
		return 0, false, 0, snapshot.ErrReleased
	}
	c := &counter{}
	recs := make([]txn.Record, len(chain))
	var prev uint64
	for i, id := range chain {
		rec, err := t.Get(id)
		c.n++
		if err != nil {
			return 0, false, c.n, err
		}
		if rec.Status == txn.Committed {
			if i > 0 && rec.Commit <= prev {
				return 0, false, c.n, ErrCorrupt
			}
			prev = rec.Commit
		}
		recs[i] = rec
	}
	for i := len(chain) - 1; i >= 0; i-- {
		if sees(s, chain[i], recs[i], c) {
			return chain[i], true, c.n, nil
		}
	}
	return 0, false, c.n, nil
}

// sees applies the derived rule: the owner's own versions are always
// visible; anything else must be committed below the watermark and
// outside the snapshot's active set.
func sees(s *snapshot.Snapshot, id txn.ID, rec txn.Record, c *counter) bool {
	if id == s.Owner() {
		return true
	}
	if rec.Status != txn.Committed || rec.Commit >= s.Watermark() {
		return false
	}
	c.n++
	return !s.InActive(id)
}
