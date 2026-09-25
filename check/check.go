// Package check provides a naive exact set used as ground truth for
// verifying the bloom package, plus all tests of this module.
package check

// Set is a naive exact set of byte-string values.
type Set struct{ m map[string]struct{} }

// NewSet returns an empty Set.
func NewSet() *Set { return &Set{m: map[string]struct{}{}} }

// Add inserts b into the set.
func (s *Set) Add(b []byte) { s.m[string(b)] = struct{}{} }

// Contains reports whether b is in the set. It is always exact.
func (s *Set) Contains(b []byte) bool {
	_, ok := s.m[string(b)]
	return ok
}
