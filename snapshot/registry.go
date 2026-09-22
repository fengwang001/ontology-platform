package snapshot

import (
	"errors"
	"sync"

	"ontology/txid"
)

// ErrTooManySnapshots is returned when the active-snapshot limit is reached.
var ErrTooManySnapshots = errors.New("snapshot: active snapshot limit reached")

// Registry owns open snapshots. It is the single authority that pairs
// "capture the next id" with "publish the snapshot": between those two steps
// no reclaim watermark can advance past the new snapshot (see DESIGN.md),
// because Open holds the registry lock while doing both atomically.
type Registry struct {
	source txid.Source

	mu      sync.Mutex
	maxOpen int // <= 0 means unlimited
	open    map[*Snapshot]struct{}
	seq     uint64
}

// NewRegistry creates a registry backed by src. maxOpen <= 0 means unlimited.
func NewRegistry(src txid.Source, maxOpen int) *Registry {
	return &Registry{source: src, maxOpen: maxOpen, open: make(map[*Snapshot]struct{})}
}

// Open establishes a snapshot in one atomic step: it reads the allocator's
// next id (the point) and publishes the snapshot under the same lock that
// ComputeWatermark uses. Therefore a reclaim pass can never advance its
// watermark "through" a snapshot that is opening concurrently: either the
// snapshot is published first (watermark <= its point) or the watermark is
// fixed first (the new point is >= the watermark). Both orders are safe.
func (r *Registry) Open(active []txid.ID) (*Snapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.maxOpen > 0 && len(r.open) >= r.maxOpen {
		return nil, ErrTooManySnapshots
	}
	point, err := r.source.Peek()
	if err != nil {
		return nil, err
	}
	s := newSnapshot(point, active, txid.Invalid)
	r.open[s] = struct{}{}
	r.seq++
	return s, nil
}

// Close removes s from the active set. Closing twice is a no-op.
func (r *Registry) Close(s *Snapshot) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.open[s]; ok {
		delete(r.open, s)
		r.seq++
	}
}

// Count returns the number of currently open snapshots.
func (r *Registry) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.open)
}

// MinPoint returns the smallest point among open snapshots and true, or false
// when no snapshot is open. It is the only input the reclaimer needs to bound
// the watermark.
// ComputeWatermark fixes the current reclaim watermark under the same mutex
// as Open. With open snapshots it is the smallest snapshot point; with none,
// it is the allocator's next id. Returning this value under the lock is what
// excludes the watermark/new-snapshot race (see Open).
func (r *Registry) ComputeWatermark() (txid.ID, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var min txid.ID
	found := false
	for s := range r.open {
		if !found || s.Point.Less(min) {
			min, found = s.Point, true
		}
	}
	if found {
		return min, true, nil
	}
	p, err := r.source.Peek()
	return p, false, err
}

// MinPoint returns the smallest point among open snapshots and true, or false
// when none is open. Read-only; it does not consult the id source.
func (r *Registry) MinPoint() (txid.ID, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var min txid.ID
	found := false
	for s := range r.open {
		if !found || s.Point.Less(min) {
			min, found = s.Point, true
		}
	}
	return min, found
}

// Epoch increments and returns the registry change counter; tests use it to
// observe open/close activity without touching internals.
func (r *Registry) Epoch() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.seq
}
