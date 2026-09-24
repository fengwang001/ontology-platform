// Package snapshot implements read snapshots: a high-water mark plus the
// set of transactions still active when the snapshot was taken.
package snapshot

import (
	"errors"
	"sync/atomic"

	"ontology/txn"
)

// ErrTooManyActive rejects snapshots whose active set exceeds MaxActive.
var ErrTooManyActive = errors.New("snapshot: active set exceeds limit")

// ErrReleased is returned when a released snapshot is used.
var ErrReleased = errors.New("snapshot: used after release")

// MaxActive bounds the active set, keeping snapshot memory O(MaxActive).
const MaxActive = 4096

// Snapshot is an immutable read snapshot, safe for concurrent reads.
type Snapshot struct {
	water    uint64
	own      txn.ID
	active   map[txn.ID]struct{}
	released atomic.Bool
}

// New builds a snapshot at high-water mark water, taken by transaction
// own, with the given active transaction set.
func New(water uint64, own txn.ID, active []txn.ID) (*Snapshot, error) {
	if len(active) > MaxActive {
		return nil, ErrTooManyActive
	}
	s := &Snapshot{water: water, own: own, active: make(map[txn.ID]struct{}, len(active))}
	for _, id := range active {
		s.active[id] = struct{}{}
	}
	return s, nil
}

// Release frees the snapshot; any later use returns ErrReleased.
func (s *Snapshot) Release() { s.released.Store(true) }

// Err reports ErrReleased once the snapshot has been released.
func (s *Snapshot) Err() error {
	if s.released.Load() {
		return ErrReleased
	}
	return nil
}

// Water returns the high-water mark: sequences in [0, water) may be visible.
func (s *Snapshot) Water() uint64 { return s.water }

// Own returns the transaction that took the snapshot.
func (s *Snapshot) Own() txn.ID { return s.own }

// Items returns the in-memory entry count: exactly the active set size,
// independent of the total number of transactions.
func (s *Snapshot) Items() int { return len(s.active) }

// InActive reports whether id was active when the snapshot was taken.
func (s *Snapshot) InActive(id txn.ID) (bool, error) {
	if err := s.Err(); err != nil {
		return false, err
	}
	_, ok := s.active[id]
	return ok, nil
}
