// Package snapshot implements read snapshots: the snapshot point plus the
// set of transactions active at snapshot establishment, and the
// left-closed/right-open visibility rule derived from snapshot isolation.
package snapshot

import "ontology/txid"

// Snapshot is an immutable read view.
//
// Point is the id the allocator would hand out next at establishment time.
// Active holds write transactions that were in flight at that moment.
type Snapshot struct {
	Point  txid.ID
	active map[txid.ID]struct{}
	self   txid.ID // optional owning write transaction (read-your-writes)
}

// newSnapshot builds a snapshot; active is copied defensively.
func newSnapshot(point txid.ID, active []txid.ID, self txid.ID) *Snapshot {
	s := &Snapshot{Point: point, self: self, active: make(map[txid.ID]struct{}, len(active))}
	for _, id := range active {
		s.active[id] = struct{}{}
	}
	return s
}

// Visible reports whether a version committed at cid is visible under the
// snapshot-isolation rule:
//
//	cid < Point  AND  cid not in Active
//
// The interval is left-closed and right-open. A commit whose cid equals
// Point is strictly later than the snapshot establishment and is invisible:
// Point was "the next id", so a commit at Point cannot have completed before
// the snapshot was taken.
func (s *Snapshot) Visible(cid txid.ID) bool {
	if !cid.Less(s.Point) {
		return false
	}
	if _, busy := s.active[cid]; busy {
		return false
	}
	return true
}

// IsSelf reports whether t is the snapshot's owning write transaction.
func (s *Snapshot) IsSelf(t txid.ID) bool { return s.self.Valid() && t == s.self }

// SelfTxn returns the owning write transaction id, or Invalid for a pure
// read snapshot.
func (s *Snapshot) SelfTxn() txid.ID { return s.self }

// WithSelf returns a derived view sharing the same point/active set but
// reading as write transaction self (used for read-your-writes).
func (s *Snapshot) WithSelf(self txid.ID) *Snapshot {
	cp := *s
	cp.self = self
	return &cp
}

// Active reports whether id was in the active set at establishment.
func (s *Snapshot) Active(id txid.ID) bool {
	_, ok := s.active[id]
	return ok
}
