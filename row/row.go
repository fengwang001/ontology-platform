// Package row defines the row type, its total order, and a
// concurrency-safe in-memory sorted set of rows.
package row

import (
	"sort"
	"sync"
)

// Row is one element of the ordered data set. The sort key is the
// composite (Score, ID): Score first, ID breaks ties. ID is unique.
type Row struct {
	Score float64
	ID    string
}

// Less reports whether a sorts strictly before b under the composite
// key (Score, ID).
func Less(a, b Row) bool {
	if a.Score != b.Score {
		return a.Score < b.Score
	}
	return a.ID < b.ID
}

// Store is a sorted set of rows safe for concurrent use.
// Rows are kept sorted by (Score, ID) at all times.
type Store struct {
	mu   sync.RWMutex
	rows []Row
}

// New returns an empty Store.
func New() *Store { return &Store{} }

// Add inserts r, keeping the set sorted. If a row with the same
// composite key already exists it is replaced.
func (s *Store) Add(r Row) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := sort.Search(len(s.rows), func(i int) bool { return !Less(s.rows[i], r) })
	if i < len(s.rows) && s.rows[i] == r {
		s.rows[i] = r
		return
	}
	s.rows = append(s.rows, Row{})
	copy(s.rows[i+1:], s.rows[i:])
	s.rows[i] = r
}

// Del removes the row with the given ID. It reports whether a row
// was removed.
func (s *Store) Del(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, r := range s.rows {
		if r.ID == id {
			s.rows = append(s.rows[:i], s.rows[i+1:]...)
			return true
		}
	}
	return false
}

// Snapshot returns a consistent copy of the sorted rows. Paging
// operates on snapshots so it is a pure function of (cursor, data).
func (s *Store) Snapshot() []Row {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Row, len(s.rows))
	copy(out, s.rows)
	return out
}

// Len returns the number of rows.
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.rows)
}
