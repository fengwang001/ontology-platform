// Package row keeps the materialized current row per key and the accumulated
// column-level changelog. It depends only on package diff.
package row

import (
	"sync"

	"ontology/diff"
)

// Store holds current rows and changelogs keyed by key. All state lives in
// process memory. The zero value is not usable; use New.
type Store struct {
	mu   sync.RWMutex
	rows map[string]map[string]string
	logs map[string][]diff.Change
}

// New returns an empty Store.
func New() *Store {
	return &Store{
		rows: map[string]map[string]string{},
		logs: map[string][]diff.Change{},
	}
}

func cloneRow(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// Apply treats after as the complete new row of key (full-row replacement),
// appends the column-level diff to the key's changelog and updates the
// current row. It returns the changes produced by this event. A fresh
// diff.Differ is used per call (a Differ is single-goroutine by contract),
// so concurrent Applies on different keys never share mutable scratch.
func (s *Store) Apply(key string, after map[string]string) []diff.Change {
	s.mu.Lock()
	defer s.mu.Unlock()
	changes := diff.New().Diff(s.rows[key], after) // nil before == empty row
	s.logs[key] = append(s.logs[key], changes...)
	s.rows[key] = cloneRow(after)
	out := make([]diff.Change, len(changes))
	copy(out, changes)
	return out
}

// View returns a deep copy of the current row of every key.
func (s *Store) View() map[string]map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]map[string]string, len(s.rows))
	for k, r := range s.rows {
		out[k] = cloneRow(r)
	}
	return out
}

// Recompute returns the one-shot full diff from the empty row to the current
// row of key. It neither reads nor modifies the accumulated changelog.
func (s *Store) Recompute(key string) []diff.Change {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return diff.New().Diff(nil, s.rows[key])
}

// Log returns a copy of the accumulated changelog of key.
func (s *Store) Log(key string) []diff.Change {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]diff.Change, len(s.logs[key]))
	copy(out, s.logs[key])
	return out
}
