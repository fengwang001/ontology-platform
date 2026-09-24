// Package name holds the in-memory namespace and name validation.
package name

import (
	"errors"
	"sort"
	"sync"
)

var (
	// ErrNotFound is returned when a rename source is absent.
	ErrNotFound = errors.New("name: source not found")
	// ErrExists is returned when a rename target is already taken.
	ErrExists = errors.New("name: target already exists")
)

// Valid reports whether s is a legal name. Any string is legal:
// the empty name is allowed and path separators are ordinary characters.
func Valid(string) bool { return true }

// Set is an in-memory namespace: a set of names guarded by a mutex.
type Set struct {
	mu sync.Mutex
	m  map[string]struct{}
}

// New returns a Set containing the given names.
func New(names ...string) *Set {
	s := &Set{m: make(map[string]struct{}, len(names))}
	for _, n := range names {
		s.m[n] = struct{}{}
	}
	return s
}

// Lock and Unlock hold the namespace for a whole batch. While locked,
// only the Locked-suffixed methods may be used; concurrent modifications
// block until Unlock.
func (s *Set) Lock()   { s.mu.Lock() }
func (s *Set) Unlock() { s.mu.Unlock() }

// Contains reports whether n is in the namespace.
func (s *Set) Contains(n string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ContainsLocked(n)
}

// ContainsLocked is Contains for callers already holding the lock.
func (s *Set) ContainsLocked(n string) bool {
	_, ok := s.m[n]
	return ok
}

// Len returns the number of names.
func (s *Set) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.m)
}

// Rename moves from to to, taking the lock.
func (s *Set) Rename(from, to string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.RenameLocked(from, to)
}

// RenameLocked is Rename for callers already holding the lock.
func (s *Set) RenameLocked(from, to string) error {
	if _, ok := s.m[from]; !ok {
		return ErrNotFound
	}
	if _, ok := s.m[to]; ok {
		return ErrExists
	}
	delete(s.m, from)
	s.m[to] = struct{}{}
	return nil
}

// Snapshot returns the sorted names.
func (s *Set) Snapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.m))
	for n := range s.m {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Equal reports whether both sets hold exactly the same names.
func (s *Set) Equal(o *Set) bool {
	a, b := s.Snapshot(), o.Snapshot()
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
