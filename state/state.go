// Package state holds the mutable map[string]int64 and the set of keys
// dirtied since the last checkpoint. It depends on no other package.
//
// A State is not safe for concurrent use; the api package serializes every
// access behind a single mutex.
package state

// State is a mutable map that tracks which keys changed since ResetDirty.
type State struct {
	m     map[string]int64
	dirty map[string]struct{}
}

// New returns an empty State.
func New() *State {
	return &State{
		m:     map[string]int64{},
		dirty: map[string]struct{}{},
	}
}

// Set writes v under k. It marks k dirty only when the live value actually
// changes; writing the same value is a no-op.
func (s *State) Set(k string, v int64) {
	if old, ok := s.m[k]; ok && old == v {
		return
	}
	s.m[k] = v
	s.dirty[k] = struct{}{}
}

// Delete removes k. Deleting a missing key is a no-op: it neither errors nor
// dirties the key. Deleting a live key marks it dirty so the next delta can
// record its tombstone.
func (s *State) Delete(k string) {
	if _, ok := s.m[k]; !ok {
		return
	}
	delete(s.m, k)
	s.dirty[k] = struct{}{}
}

// Get reports the current value of k.
func (s *State) Get(k string) (int64, bool) {
	v, ok := s.m[k]
	return v, ok
}

// Len returns the number of live keys.
func (s *State) Len() int { return len(s.m) }

// Clone returns a deep copy of the live map.
func (s *State) Clone() map[string]int64 {
	out := make(map[string]int64, len(s.m))
	for k, v := range s.m {
		out[k] = v
	}
	return out
}

// Dirty returns the set of keys touched since the last ResetDirty. Callers
// must not mutate it (it is consumed by snap while the api lock is held).
func (s *State) Dirty() map[string]struct{} { return s.dirty }

// ResetDirty empties the dirty-key set after a checkpoint has consumed it.
func (s *State) ResetDirty() { s.dirty = map[string]struct{}{} }
