package stackvm

import "sync"

// journalEntry records one storage mutation so a failing frame (and every
// frame it called) can be undone.
type journalEntry struct {
	key     int64
	existed bool
	old     int64
}

// Storage is the key/value state shared by every submitted program.
// A single mutex serializes whole executions: a reader through Snapshot can
// therefore only ever observe a complete before- or after-execution state.
type Storage struct {
	// Mu is held for the duration of a whole execution so external readers
	// through Snapshot can only see a complete before/after state.
	Mu sync.Mutex
	kv map[int64]int64
}

// NewStorage creates an empty shared storage.
func NewStorage() *Storage {
	return &Storage{kv: make(map[int64]int64)}
}

// Snapshot returns a point-in-time deep copy of the storage.
func (s *Storage) Snapshot() map[int64]int64 {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	return s.copyLocked()
}

func (s *Storage) copyLocked() map[int64]int64 {
	out := make(map[int64]int64, len(s.kv))
	for k, v := range s.kv {
		out[k] = v
	}
	return out
}

// The methods below must be called while holding the machine-wide lock.

// read returns the value at key (0 when unset) and never mutates storage.
func (s *Storage) read(key int64) int64 {
	return s.kv[key]
}

// write stores value at key and returns a journal entry for rollback.
func (s *Storage) write(key, value int64) journalEntry {
	old, existed := s.kv[key]
	s.kv[key] = value
	return journalEntry{key: key, existed: existed, old: old}
}

// undo reverses journal entries in reverse order. Entries from descendant
// frames are appended first, so undoing the concatenated journal of a frame
// removes the frame's own writes together with all of its descendants'.
func (s *Storage) undo(journal []journalEntry) {
	for i := len(journal) - 1; i >= 0; i-- {
		e := journal[i]
		if e.existed {
			s.kv[e.key] = e.old
		} else {
			delete(s.kv, e.key)
		}
	}
}
