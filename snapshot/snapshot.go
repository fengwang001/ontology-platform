// Package snapshot models read snapshots for snapshot isolation.
//
// A Snapshot is taken at one logical instant: it records a snapshot point
// (the transaction numbering frontier at that instant) and the set of
// transactions still in flight. Snapshots are immutable once taken, which is
// exactly what makes repeatable reads repeatable: every read made through the
// same Snapshot applies the same visibility test forever.
package snapshot

import "ontology/txid"

// Snapshot is an immutable read view.
type Snapshot struct {
	point  txid.TxID
	active map[txid.TxID]struct{}
}

// New builds a snapshot. point is the numbering frontier: every committed
// transaction number strictly below point was finalized before this snapshot
// was taken. active is the in-flight transaction set; its contents are
// copied so later mutation by the caller cannot change the snapshot.
func New(point txid.TxID, active map[txid.TxID]struct{}) *Snapshot {
	copied := make(map[txid.TxID]struct{}, len(active))
	for id := range active {
		copied[id] = struct{}{}
	}
	return &Snapshot{point: point, active: copied}
}

// Point returns the snapshot's frontier transaction number.
func (s *Snapshot) Point() txid.TxID { return s.point }

// Active reports whether transaction id was in flight when the snapshot
// was taken.
func (s *Snapshot) Active(id txid.TxID) bool {
	_, ok := s.active[id]
	return ok
}

// Visible reports whether a version committed by transaction id is visible
// through this snapshot.
//
// The boundary is left-closed/right-open on purpose: a transaction is
// visible iff it committed strictly before the snapshot point and was not in
// the snapshot's active set. A commit whose number equals the point happened
// at or after the snapshot instant, so it must be invisible.
func (s *Snapshot) Visible(id txid.TxID) bool {
	if id == txid.Zero {
		return false
	}
	if !(id < s.point) {
		return false
	}
	if _, inFlight := s.active[id]; inFlight {
		return false
	}
	return true
}
