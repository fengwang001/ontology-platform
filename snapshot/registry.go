package snapshot

import (
	"errors"
	"sync"

	"ontology/txid"
)

// ErrSnapshotLimit is returned when opening another snapshot would exceed
// the configured maximum number of concurrently open snapshots.
var ErrSnapshotLimit = errors.New("snapshot: concurrent snapshot limit reached")

// Registry hands out snapshots and remembers which are still open.
//
// All methods are safe for concurrent use. The registry owns no version data;
// it only answers the visibility-frontier question the reclaimer needs:
// "could any open snapshot still see this committed transaction?".
type Registry struct {
	mu       sync.Mutex
	src      txid.Source
	open     map[*Snapshot]struct{}
	maxOpen  int
	inflight map[txid.TxID]struct{}
}

// NewRegistry builds a registry. src supplies the numbering frontier for
// freshly taken snapshots; maxOpen <= 0 means unbounded.
func NewRegistry(src txid.Source, maxOpen int) *Registry {
	return &Registry{
		src:      src,
		open:     make(map[*Snapshot]struct{}),
		maxOpen:  maxOpen,
		inflight: make(map[txid.TxID]struct{}),
	}
}

// SetInFlight replaces the set of currently active writing transactions.
// It is consulted when the next snapshot is taken.
func (r *Registry) SetInFlight(ids map[txid.TxID]struct{}) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.inflight = make(map[txid.TxID]struct{}, len(ids))
	for id := range ids {
		r.inflight[id] = struct{}{}
	}
}

// Begin takes a new snapshot and records it as open.
func (r *Registry) Begin() (*Snapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.maxOpen > 0 && len(r.open) >= r.maxOpen {
		return nil, ErrSnapshotLimit
	}
	frontier, err := r.src.Next()
	if err != nil {
		return nil, err
	}
	s := New(frontier, r.inflight)
	r.open[s] = struct{}{}
	return s, nil
}

// End closes a snapshot previously returned by Begin. Closing an unknown or
// already closed snapshot is a no-op.
func (r *Registry) End(s *Snapshot) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.open, s)
}

// OpenCount returns the number of currently open snapshots.
func (r *Registry) OpenCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.open)
}

// horizonLocked returns the smallest snapshot point among open snapshots.
// With no open snapshot the frontier is "infinity", represented by the max
// identifier, meaning every committed transaction is behind every reader.
func (r *Registry) horizonLocked() txid.TxID {
	h := txid.TxID(^uint64(0))
	for s := range r.open {
		if p := s.point; p < h {
			h = p
		}
	}
	return h
}

// Horizon returns the smallest point among open snapshots.
func (r *Registry) Horizon() txid.TxID {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.horizonLocked()
}

// GloballyVisible reports whether committed transaction id is visible to
// every open snapshot (and therefore could never be needed by a reader
// again). A transaction is globally visible when it is below the smallest
// open snapshot point and absent from every open snapshot's active set.
func (r *Registry) GloballyVisible(id txid.TxID) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if id == txid.Zero {
		return false
	}
	for s := range r.open {
		if !s.Visible(id) {
			return false
		}
	}
	return true
}
