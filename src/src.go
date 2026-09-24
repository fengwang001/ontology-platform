// Package src is a single source partition: an idempotent element set.
// It depends on no other package of this module.
package src

// Set is the set of elements held by one partition. It is not safe for
// concurrent use; callers at the uni layer provide synchronization.
type Set struct {
	m map[string]struct{}
}

// New creates an empty partition set.
func New() *Set { return &Set{m: make(map[string]struct{})} }

// Add inserts e. It reports whether the set changed, i.e. whether e was
// absent before — this is exactly the per-partition idempotency test.
func (s *Set) Add(e string) bool {
	if _, ok := s.m[e]; ok {
		return false
	}
	s.m[e] = struct{}{}
	return true
}

// Remove deletes e. It reports whether e was present before, so the caller
// can adjust the global reference count exactly once per real withdrawal.
func (s *Set) Remove(e string) bool {
	if _, ok := s.m[e]; !ok {
		return false
	}
	delete(s.m, e)
	return true
}

// Contains reports membership.
func (s *Set) Contains(e string) bool {
	_, ok := s.m[e]
	return ok
}

// Len reports the number of held elements.
func (s *Set) Len() int { return len(s.m) }

// Members returns a freshly allocated slice of all held elements.
func (s *Set) Members() []string {
	out := make([]string, 0, len(s.m))
	for e := range s.m {
		out = append(out, e)
	}
	return out
}
