// Package kv holds committed key/value state, the global commit version,
// and the retained read/write sets of every committed transaction.
// It depends on no other package.
package kv

import "sync"

// Record is one committed transaction: its assigned version and the
// read/write sets it had at commit. Read sets are retained indefinitely so
// later transactions can detect rw conflicts.
type Record struct {
	Version int
	Read    map[string]string
	Write   map[string]string
}

// Store is the process-wide committed state. All access is serialized by mu,
// so Snapshot/Apply/Committed are safe for concurrent goroutines.
type Store struct {
	mu      sync.Mutex
	values  map[string]string // committed key/value state
	version int               // global commit version, starts at 0
	records map[int]*Record   // every committed transaction, keyed by version
	// Conflict indexes (the point of the exercise: locate conflicts by key
	// instead of scanning records):
	readers map[string]int // key -> number of committed txns that read it
	writer  map[string]int // key -> largest version of a committed txn that wrote it
}

// New returns a store at version 0 holding the given pre-existing committed
// values. Seed data is initial state, not a committed transaction: it carries
// no read/write record and leaves the global version at 0.
func New(seed map[string]string) *Store {
	values := make(map[string]string, len(seed))
	for k, v := range seed {
		values[k] = v
	}
	return &Store{
		values:  values,
		records: map[int]*Record{},
		readers: map[string]int{},
		writer:  map[string]int{},
	}
}

// Snapshot returns a copy of the committed values and the current version.
func (s *Store) Snapshot() (map[string]string, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := make(map[string]string, len(s.values))
	for k, v := range s.values {
		snap[k] = v
	}
	return snap, s.version
}

// Conflicts decides, via indexes only, whether a transaction with the given
// read/write sets taken at snapVersion has rw edges against ANY committed txn:
//   - in:  some committed U has U.Read ∩ writeSet != ∅        (edge U -> txn)
//   - out: some committed U (version > snapVersion) has
//     U.Write ∩ readSet != ∅                                    (edge txn -> U)
//
// The indexes answer both existence questions directly by key; no retained
// transaction record is ever opened or scanned.
func (s *Store) Conflicts(snapVersion int, readSet, writeSet map[string]string) (in, out bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range writeSet {
		if s.readers[k] > 0 {
			in = true
		}
	}
	for k := range readSet {
		if s.writer[k] > snapVersion {
			out = true
		}
	}
	return in, out
}

// Apply commits r: assigns the next monotonic version, applies the write set,
// retains the record, and maintains both indexes. The conflict decision is
// made by the caller before Apply is ever reached.
func (s *Store) Apply(r *Record) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.version++
	r.Version = s.version
	for k, v := range r.Write {
		s.values[k] = v
		s.writer[k] = r.Version
	}
	for k := range r.Read {
		s.readers[k]++
	}
	s.records[r.Version] = r
}

// Committed returns a copy of the committed key/value state.
func (s *Store) Committed() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make(map[string]string, len(s.values))
	for k, v := range s.values {
		cp[k] = v
	}
	return cp
}
