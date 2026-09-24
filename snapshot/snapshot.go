// Package snapshot defines read snapshots: a watermark plus the set of
// transactions still active when the snapshot was taken.
package snapshot

import (
	"errors"
	"sync/atomic"

	"ontology/txn"
)

// MaxActive bounds the active set size a snapshot may hold.
const MaxActive = 1024

var (
	// ErrTooManyActive is returned when the active set exceeds MaxActive.
	ErrTooManyActive = errors.New("snapshot: active set exceeds limit")
	// ErrReleased is returned when a released snapshot is used.
	ErrReleased = errors.New("snapshot: use after release")
)

// Snapshot is a read snapshot. Versions with commit number in
// [0, Watermark) written by transactions outside the active set are
// visible; the owner's own versions are always visible to it.
type Snapshot struct {
	wm       uint64
	owner    txn.ID
	active   map[txn.ID]struct{}
	released atomic.Bool
}

// New builds a Snapshot. owner is the snapshot's own transaction
// (0 means none). It fails with ErrTooManyActive beyond MaxActive.
func New(wm uint64, owner txn.ID, active []txn.ID) (*Snapshot, error) {
	if len(active) > MaxActive {
		return nil, ErrTooManyActive
	}
	s := &Snapshot{wm: wm, owner: owner, active: make(map[txn.ID]struct{}, len(active))}
	for _, id := range active {
		s.active[id] = struct{}{}
	}
	return s, nil
}

// Watermark returns the exclusive upper bound of visible commit numbers.
func (s *Snapshot) Watermark() uint64 { return s.wm }

// Owner returns the snapshot's own transaction ID.
func (s *Snapshot) Owner() txn.ID { return s.owner }

// InActive reports whether id was active when the snapshot was taken.
func (s *Snapshot) InActive(id txn.ID) bool { _, ok := s.active[id]; return ok }

// Items returns the number of in-memory items the snapshot holds; it
// equals the active set size and is independent of the table size.
func (s *Snapshot) Items() int { return len(s.active) }

// Release marks the snapshot released; later use yields ErrReleased.
func (s *Snapshot) Release() { s.released.Store(true) }

// Released reports whether the snapshot has been released.
func (s *Snapshot) Released() bool { return s.released.Load() }
