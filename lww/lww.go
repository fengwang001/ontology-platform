// Package lww maintains the global last-write-wins value per key over
// accepted events (max TS wins; ties go to the later arrival) and counts
// dropped (late) events. It depends on aln for lateness and acceptance.
package lww

import "ontology/aln"

type entry struct {
	ts int64
	v  int
}

// Store is not safe for concurrent use; callers (package api) serialize.
type Store struct {
	al      *aln.Aligner
	vals    map[string]entry
	dropped int
}

// New binds a Store to an Aligner.
func New(al *aln.Aligner) *Store { return &Store{al: al, vals: map[string]entry{}} }

// Feed classifies one event against the aligner: a late event is dropped
// (counted, touching nothing else); an accepted event updates the aligner's
// running min and the key's LWW value. Returns true when accepted.
func (s *Store) Feed(key string, ts int64, v int) bool {
	if s.al.Late(ts) {
		s.dropped++
		return false
	}
	s.al.Accept(ts)
	// Max TS wins; on equal TS the later arrival overwrites (>=).
	if e, ok := s.vals[key]; !ok || ts >= e.ts {
		s.vals[key] = entry{ts: ts, v: v}
	}
	return true
}

// Value returns the current final value for key.
func (s *Store) Value(key string) (int, bool) {
	e, ok := s.vals[key]
	return e.v, ok
}

// Dropped returns the number of late (discarded) events so far.
func (s *Store) Dropped() int { return s.dropped }
