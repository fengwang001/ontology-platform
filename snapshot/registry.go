package snapshot

import (
	"errors"
	"sync"

	"ontology/txid"
)

// ErrSnapshotLimit is returned when opening a snapshot would exceed the
// configured maximum number of concurrently open snapshots.
var ErrSnapshotLimit = errors.New("snapshot: active snapshot limit reached")

// Registry owns the lifecycle of open snapshots.
//
// The store serializes Open/Close/Horizon with transaction commits under its
// own lock; the registry's mutex makes the type safe on its own as well.
type Registry struct {
	mu      sync.Mutex
	maxOpen int
	nextID  uint64
	open    map[uint64]*Snapshot
}

// NewRegistry creates a registry. A non-positive limit means unlimited.
func NewRegistry(maxOpen int) *Registry {
	return &Registry{maxOpen: maxOpen, open: map[uint64]*Snapshot{}}
}

// Open creates a snapshot. point is the current txid high-water mark; active
// is the set of transactions in flight (both open writers and, defensively,
// any other ids the caller considers uncommitted). The caller must ensure
// point and active are sampled atomically with respect to commits.
func (r *Registry) Open(point txid.TxID, active map[txid.TxID]struct{}) (*Snapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.maxOpen > 0 && len(r.open) >= r.maxOpen {
		return nil, ErrSnapshotLimit
	}
	r.nextID++
	snap := &Snapshot{
		id:     r.nextID,
		point:  point,
		active: copyActive(active),
	}
	r.open[snap.id] = snap
	return snap, nil
}

// Close releases a snapshot. Closing twice or closing an unknown id is a
// no-op, so callers may defer Close safely.
func (r *Registry) Close(s *Snapshot) {
	if s == nil {
		return
	}
	r.mu.Lock()
	delete(r.open, s.id)
	r.mu.Unlock()
}

// Count returns the number of currently open snapshots.
func (r *Registry) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.open)
}

// Horizon returns the smallest visibility boundary over all open snapshots
// together with the union of their active sets, plus any extra active ids
// supplied by the caller (currently open writers). A committed transaction
// strictly smaller than the returned boundary is visible to EVERY open
// snapshot; it is also globally safe once absent from the returned union.
//
// With no open snapshots the boundary is the zero id: callers interpret this
// as "no old reader exists" and may reclaim up to the current frontier.
func (r *Registry) Horizon(extra map[txid.TxID]struct{}) (txid.TxID, map[txid.TxID]struct{}) {
	r.mu.Lock()
	defer r.mu.Unlock()
	union := copyActive(extra)
	var horizon txid.TxID
	for _, s := range r.open {
		for id := range s.active {
			union[id] = struct{}{}
		}
		if horizon == 0 || s.point.Before(horizon) {
			horizon = s.point
		}
	}
	return horizon, union
}

// SetLimit updates the concurrent-snapshot limit.
func (r *Registry) SetLimit(n int) {
	r.mu.Lock()
	r.maxOpen = n
	r.mu.Unlock()
}
