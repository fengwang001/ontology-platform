// Package snapshot implements read snapshots used for snapshot isolation.
//
// A snapshot freezes the set of in-flight (active) transactions at one point.
// Visibility is half-open: a version committed by transaction c is visible to
// snapshot S iff c < S.point AND c is not in S.active. A commit whose id is
// exactly S.point happened at or after the snapshot and is therefore not
// visible.
package snapshot

import "ontology/txid"

// Snapshot is an immutable read view.
type Snapshot struct {
	id     uint64
	point  txid.TxID
	active map[txid.TxID]struct{}
}

// Point returns the snapshot point (the high-water mark observed when the
// snapshot was established).
func (s *Snapshot) Point() txid.TxID { return s.point }

// ID returns the snapshot's identifier within its registry.
func (s *Snapshot) ID() uint64 { return s.id }

// CommittedVisible reports whether a version committed by commit is visible:
// commit must be strictly below the point and absent from the active set.
func (s *Snapshot) CommittedVisible(commit txid.TxID) bool {
	if !commit.Valid() || !s.point.Valid() {
		return false
	}
	if !commit.Before(s.point) {
		return false
	}
	_, active := s.active[commit]
	return !active
}

// Active reports whether tx was in the active set at snapshot time.
func (s *Snapshot) Active(tx txid.TxID) bool {
	_, ok := s.active[tx]
	return ok
}

// ActiveCount returns the size of the frozen active set.
func (s *Snapshot) ActiveCount() int { return len(s.active) }

// copyActive returns a fresh set with the given ids.
func copyActive(in map[txid.TxID]struct{}) map[txid.TxID]struct{} {
	out := make(map[txid.TxID]struct{}, len(in))
	for id := range in {
		out[id] = struct{}{}
	}
	return out
}
