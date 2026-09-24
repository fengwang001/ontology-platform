// Package snapshot defines read snapshots: a high-water mark plus the
// set of transactions still active when the snapshot was taken.
package snapshot

import (
	"errors"
	"fmt"
	"sync/atomic"

	"ontology/txn"
)

// MaxActive bounds the active set size; construction fails beyond it.
const MaxActive = 4096

var (
	ErrTooManyActive = errors.New("snapshot: active set exceeds limit")
	ErrReleased      = errors.New("snapshot: use after release")
)

// Snapshot is an immutable read snapshot, safe for concurrent reads
// until Release is called.
type Snapshot struct {
	hwm      uint64
	owner    txn.ID
	active   map[txn.ID]struct{}
	released atomic.Bool
}

// New builds a snapshot. owner is the snapshot's own transaction;
// active lists the transactions in flight when it was taken.
func New(hwm uint64, owner txn.ID, active []txn.ID) (*Snapshot, error) {
	if len(active) > MaxActive {
		return nil, fmt.Errorf("%w: %d > %d", ErrTooManyActive, len(active), MaxActive)
	}
	set := make(map[txn.ID]struct{}, len(active))
	for _, id := range active {
		set[id] = struct{}{}
	}
	return &Snapshot{hwm: hwm, owner: owner, active: set}, nil
}

// Release frees the snapshot; any later use returns ErrReleased.
func (s *Snapshot) Release() { s.released.Store(true) }

// HWM returns the high-water mark: commit seqs >= it are invisible.
func (s *Snapshot) HWM() (uint64, error) {
	if s.released.Load() {
		return 0, ErrReleased
	}
	return s.hwm, nil
}

// Owner returns the snapshot's own transaction ID.
func (s *Snapshot) Owner() txn.ID { return s.owner }

// Active reports whether id was in flight when the snapshot was taken.
func (s *Snapshot) Active(id txn.ID) (bool, error) {
	if s.released.Load() {
		return false, ErrReleased
	}
	_, ok := s.active[id]
	return ok, nil
}

// MemItems is the number of memory entries held: the active set size,
// independent of the total transaction count.
func (s *Snapshot) MemItems() int { return len(s.active) }
