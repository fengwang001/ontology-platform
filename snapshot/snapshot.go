// Package snapshot defines a read snapshot: a high watermark plus the set
// of transactions still active when the snapshot was taken.
package snapshot

import (
	"errors"
	"sync/atomic"

	"ontology/txn"
)

var (
	// ErrTooManyActive is returned when the active set exceeds MaxActive.
	ErrTooManyActive = errors.New("snapshot: active set exceeds limit")
	// ErrReleased is returned when a released snapshot is used again.
	ErrReleased = errors.New("snapshot: use after release")
)

// MaxActive bounds the active-set size a snapshot may hold.
const MaxActive = 4096

// Snapshot is an immutable read snapshot; safe for concurrent reads.
type Snapshot struct {
	owner    txn.ID
	wm       uint64
	active   map[txn.ID]struct{}
	released atomic.Bool
}

// New builds a snapshot owned by owner, with high watermark wm and the
// given set of still-active transactions.
func New(owner txn.ID, wm uint64, active []txn.ID) (*Snapshot, error) {
	if len(active) > MaxActive {
		return nil, ErrTooManyActive
	}
	s := &Snapshot{owner: owner, wm: wm, active: make(map[txn.ID]struct{}, len(active))}
	for _, id := range active {
		s.active[id] = struct{}{}
	}
	return s, nil
}

// Owner returns the transaction that took the snapshot; its own writes
// are always visible to it.
func (s *Snapshot) Owner() txn.ID { return s.owner }

// Watermark returns the high watermark: commit numbers must be strictly
// below it to be visible (half-open interval).
func (s *Snapshot) Watermark() uint64 { return s.wm }

// Len returns the number of in-memory items held by the snapshot, which
// equals the active-set size and never grows with the transaction total.
func (s *Snapshot) Len() int { return len(s.active) }

// Active reports whether id was still active when the snapshot was taken.
func (s *Snapshot) Active(id txn.ID) bool {
	_, ok := s.active[id]
	return ok
}

// Release marks the snapshot released; later use returns ErrReleased.
func (s *Snapshot) Release() { s.released.Store(true) }

// Released reports whether Release has been called.
func (s *Snapshot) Released() bool { return s.released.Load() }
