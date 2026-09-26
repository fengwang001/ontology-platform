// Package rsv manages reservation identities on top of the res core:
// monotonic id allocation, request validation and Release checks.
package rsv

import (
	"errors"
	"sync"

	"ontology/res"
)

// Distinct sentinel errors for the three failure modes owned by this layer.
var (
	ErrNeed     = errors.New("rsv: need must satisfy 1 <= need <= capacity")
	ErrInterval = errors.New("rsv: end must be greater than start")
	ErrRelease  = errors.New("rsv: id is not an active reservation")
)

// Manager is the concurrency-safe reservation manager.
type Manager struct {
	mu       sync.Mutex
	capacity int64
	set      res.Set
	byID     map[int64]res.R
	nextID   int64
}

// NewManager creates a manager for the given (positive) capacity.
func NewManager(capacity int64) *Manager {
	return &Manager{capacity: capacity, byID: make(map[int64]res.R)}
}

// Reserve validates the request, then accepts it iff peak load over
// [start, end) plus need stays within capacity. An id is allocated for every
// well-formed call (so call numbering is stable), but a capacity rejection
// records no reservation. Validation failures touch no state at all.
func (m *Manager) Reserve(start, end, need int64) (int64, bool, error) {
	if need < 1 || need > m.capacity {
		return 0, false, ErrNeed
	}
	if end <= start {
		return 0, false, ErrInterval
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	id := m.nextID
	if m.set.Peak(start, end)+need > m.capacity {
		return id, false, nil
	}
	r := res.R{Start: start, End: end, Need: need, ID: id}
	m.set.Add(r)
	m.byID[id] = r
	return id, true, nil
}

// Release deactivates id; it is an error to release a missing or released id.
func (m *Manager) Release(id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.byID[id]
	if !ok {
		return ErrRelease
	}
	m.set.Remove(r)
	delete(m.byID, id)
	return nil
}

// Active returns the number of currently active reservations.
func (m *Manager) Active() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.byID)
}

// Peak reports the current peak load over [start, end) via the res core.
func (m *Manager) Peak(start, end int64) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.set.Peak(start, end)
}
