package vm

import "sync"

// Store is shared key/value storage. Executions may be submitted concurrently;
// a single mutex makes them take effect serially, and readers only ever observe
// a complete pre- or post-execution state (committed snapshots).
type Store struct {
	mu sync.RWMutex
	kv map[int64]int64
}

// NewStore creates an empty shared store.
func NewStore() *Store {
	return &Store{kv: make(map[int64]int64)}
}

// snapshotLocked returns a deep copy of committed storage; caller holds mu.
func (s *Store) snapshotLocked() map[int64]int64 {
	out := make(map[int64]int64, len(s.kv))
	for k, v := range s.kv {
		out[k] = v
	}
	return out
}

// Snapshot returns a complete deep copy of committed storage (a consistent
// whole-store view, never a half-applied execution).
func (s *Store) Snapshot() map[int64]int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshotLocked()
}

// Get reads one committed key (absent keys read as 0).
func (s *Store) Get(key int64) int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.kv[key]
}

// begin takes a committed snapshot under the write lock. All writes of one
// execution stay in the returned overlay until Commit; Rollback/abandon drops
// them, so failures never leak partial state.
func (s *Store) begin() map[int64]int64 {
	s.mu.Lock()
	return s.snapshotLocked()
}

func (s *Store) commit(writes map[int64]int64) {
	s.kv = writes
	s.mu.Unlock()
}

func (s *Store) rollback() {
	s.mu.Unlock()
}
