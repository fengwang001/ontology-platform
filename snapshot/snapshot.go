// Package snapshot holds a read snapshot: a high-water mark plus the set
// of transactions that were still active when the snapshot was taken.
package snapshot

import (
	"errors"
	"sync/atomic"

	"ontology/txn"
)

// MaxActive bounds the active set; construction beyond it fails.
const MaxActive = 4096

var (
	// ErrTooManyActive is returned when the active set exceeds MaxActive.
	ErrTooManyActive = errors.New("snapshot: active set exceeds limit")
	// ErrSnapshotReleased is returned when a released snapshot is used.
	ErrSnapshotReleased = errors.New("snapshot: used after release")
)

// Snapshot is immutable after construction except for Release, so it is
// safe for concurrent read-only use.
type Snapshot struct {
	tab      *txn.Table
	hiWater  uint64
	self     txn.ID
	active   map[txn.ID]struct{}
	released atomic.Bool
}

// New copies the active set; memory grows with len(active), not with the
// number of transactions in the table.
func New(tab *txn.Table, hiWater uint64, self txn.ID, active []txn.ID) (*Snapshot, error) {
	if len(active) > MaxActive {
		return nil, ErrTooManyActive
	}
	s := &Snapshot{
		tab:     tab,
		hiWater: hiWater,
		self:    self,
		active:  make(map[txn.ID]struct{}, len(active)),
	}
	for _, id := range active {
		s.active[id] = struct{}{}
	}
	return s, nil
}

// Release marks the snapshot unusable; later checks fail with
// ErrSnapshotReleased.
func (s *Snapshot) Release() { s.released.Store(true) }

// Released reports whether Release has been called.
func (s *Snapshot) Released() bool { return s.released.Load() }

func (s *Snapshot) HiWater() uint64   { return s.hiWater }
func (s *Snapshot) Self() txn.ID      { return s.self }
func (s *Snapshot) Table() *txn.Table { return s.tab }

// ActiveItems is the number of in-memory items the snapshot holds.
func (s *Snapshot) ActiveItems() int { return len(s.active) }

// IsActive reports whether id was active when the snapshot was taken.
func (s *Snapshot) IsActive(id txn.ID) bool {
	_, ok := s.active[id]
	return ok
}
