// Package mset maintains a single group's multiset: Val -> positive multiplicity.
// The distinct count is updated incrementally on 0<->1 crossings; entries whose
// multiplicity reaches zero are deleted immediately.
package mset

// M is one group's multiset. It is not safe for concurrent use; callers
// (package dagg) provide the synchronization.
type M struct {
	m        map[string]int
	distinct int
}

// New returns an empty multiset.
func New() *M { return &M{m: make(map[string]int)} }

// Add inserts one occurrence of v. crossedUp is true exactly on a 0->1
// crossing, i.e. when the distinct count grows by one.
func (s *M) Add(v string) (crossedUp bool) {
	if s.m[v] == 0 {
		s.distinct++
		crossedUp = true
	}
	s.m[v]++
	return crossedUp
}

// Remove withdraws one occurrence of v. ok is false when v has mult==0
// (withdrawal of an absent value); state is then untouched. crossedDown is
// true exactly on a 1->0 crossing, in which case the entry is deleted.
func (s *M) Remove(v string) (crossedDown, ok bool) {
	switch s.m[v] {
	case 0:
		return false, false
	case 1:
		delete(s.m, v)
		s.distinct--
		return true, true
	default:
		s.m[v]--
		return false, true
	}
}

// Distinct is the number of Vals with mult>0.
func (s *M) Distinct() int { return s.distinct }

// Len is the number of live entries (mult>0).
func (s *M) Len() int { return len(s.m) }

// Mult reports the multiplicity of v; absent values read as 0.
func (s *M) Mult(v string) int { return s.m[v] }

// Snapshot returns a copy of the live Val -> mult map.
func (s *M) Snapshot() map[string]int {
	out := make(map[string]int, len(s.m))
	for v, n := range s.m {
		out[v] = n
	}
	return out
}
