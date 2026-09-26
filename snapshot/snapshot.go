// Package snapshot provides read-only snapshot handles over a store.Store.
// A snapshot pins the store version at open time; values overwritten while
// the snapshot is open are preserved (copy-on-write) so the snapshot always
// observes the data as of its version. Preserved values are released when
// the snapshot is closed.
package snapshot

import (
	"errors"
	"sync"
	"sync/atomic"

	"ontology/store"
)

// ErrSnapshotClosed is returned by Get on a closed snapshot.
var ErrSnapshotClosed = errors.New("snapshot: closed")

// Manager tracks open snapshots and preserves overwritten values for them.
type Manager struct {
	st    *store.Store
	mu    sync.Mutex
	snaps map[*Snapshot]struct{}
}

// NewManager attaches a Manager to st via a write hook.
func NewManager(st *store.Store) *Manager {
	m := &Manager{st: st, snaps: make(map[*Snapshot]struct{})}
	st.AddHook(m.onWrite)
	return m
}

// Open captures the current store version and returns a snapshot of it.
func (m *Manager) Open() *Snapshot {
	s := &Snapshot{m: m, version: m.st.Version(), preserved: make(map[string][]byte)}
	m.mu.Lock()
	m.snaps[s] = struct{}{}
	m.mu.Unlock()
	return s
}

// onWrite is the COW trigger: called by the store under its write lock just
// before an overwrite. It preserves the old value for every open snapshot
// that still sees it (1 <= oldVersion <= snapshot version).
func (m *Manager) onWrite(key string, oldValue []byte, oldVersion, newVersion uint64) {
	if oldVersion == 0 {
		return // key did not exist before; nothing visible to preserve
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for s := range m.snaps {
		if oldVersion > s.version {
			continue // snapshot predates this value; older one already kept
		}
		if _, ok := s.preserved[key]; !ok {
			s.preserved[key] = oldValue
		}
	}
}

// Snapshot is a read-only, consistent view of the store at one version.
type Snapshot struct {
	m         *Manager
	version   uint64
	preserved map[string][]byte // guarded by m.mu
	closed    atomic.Bool
	reads     atomic.Int64
}

// Version returns the snapshot's version watermark.
func (s *Snapshot) Version() uint64 { return s.version }

// Closed reports whether the snapshot has been closed.
func (s *Snapshot) Closed() bool { return s.closed.Load() }

// Get returns the value of key as of the snapshot version. Preserved
// (overwritten) values win; otherwise the versioned store is consulted.
func (s *Snapshot) Get(key string) ([]byte, bool, error) {
	s.reads.Add(1)
	if s.closed.Load() {
		return nil, false, ErrSnapshotClosed
	}
	s.m.mu.Lock()
	v, ok := s.preserved[key]
	s.m.mu.Unlock()
	if ok {
		return append([]byte(nil), v...), true, nil
	}
	v, ok = s.m.st.GetAt(key, s.version)
	return v, ok, nil
}

// Keys returns the sorted keys visible at the snapshot version.
func (s *Snapshot) Keys() []string { return s.m.st.KeysAt(s.version) }

// ReadCount returns how many Get calls this snapshot has served.
func (s *Snapshot) ReadCount() int64 { return s.reads.Load() }

// PreservedCount returns how many overwritten values are kept for this
// snapshot. It drops to zero after Close.
func (s *Snapshot) PreservedCount() int {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	return len(s.preserved)
}

// Close releases every value preserved for this snapshot.
func (s *Snapshot) Close() {
	s.m.mu.Lock()
	delete(s.m.snaps, s)
	s.preserved = make(map[string][]byte)
	s.m.mu.Unlock()
	s.closed.Store(true)
}
