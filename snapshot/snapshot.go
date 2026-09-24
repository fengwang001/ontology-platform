// Package snapshot implements read snapshots: a snapshot point plus the
// set of write transactions that were active when the snapshot was taken.
package snapshot

import (
	"sync"
	"time"

	"ontology/txid"
)

// Snapshot is an immutable read view. A version committed by transaction t
// is visible to the snapshot iff t < Point and t was not active at Open.
type Snapshot struct {
	Point   txid.ID
	Created time.Time
	active  map[txid.ID]struct{}
	mgr     *Manager
	id      uint64
	once    sync.Once
}

// Eligible reports whether a version committed by t is visible.
func (s *Snapshot) Eligible(t txid.ID) bool {
	if !t.Valid() || t >= s.Point {
		return false
	}
	_, active := s.active[t]
	return !active
}

// Close releases the snapshot. It is idempotent.
func (s *Snapshot) Close() { s.once.Do(func() { s.mgr.release(s.id) }) }

// Manager allocates transaction IDs and tracks active transactions and
// open snapshots. All methods are safe for concurrent use.
type Manager struct {
	mu     sync.Mutex
	alloc  *txid.Allocator
	now    func() time.Time
	active map[txid.ID]struct{}
	snaps  map[uint64]*Snapshot
	seq    uint64
}

// NewManager returns a Manager that stamps snapshots with now.
func NewManager(now func() time.Time) *Manager {
	return &Manager{
		alloc:  txid.NewAllocator(),
		now:    now,
		active: map[txid.ID]struct{}{},
		snaps:  map[uint64]*Snapshot{},
	}
}

// BeginTx allocates a new transaction ID and marks it active.
func (m *Manager) BeginTx() txid.ID {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := m.alloc.Next()
	m.active[id] = struct{}{}
	return id
}

// EndTx marks a transaction as no longer active.
func (m *Manager) EndTx(id txid.ID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.active, id)
}

// Open atomically captures the snapshot point and the active set.
func (m *Manager) Open() *Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	active := make(map[txid.ID]struct{}, len(m.active))
	for id := range m.active {
		active[id] = struct{}{}
	}
	m.seq++
	s := &Snapshot{Point: m.alloc.Curr(), Created: m.now(), active: active, mgr: m, id: m.seq}
	m.snaps[s.id] = s
	return s
}

func (m *Manager) release(id uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.snaps, id)
}

// List returns the currently open snapshots.
func (m *Manager) List() []*Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Snapshot, 0, len(m.snaps))
	for _, s := range m.snaps {
		out = append(out, s)
	}
	return out
}

// Count returns the number of open snapshots.
func (m *Manager) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.snaps)
}
