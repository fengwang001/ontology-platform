// Package stg implements the staging area: an ordered buffer of pending
// upserts and deletes, drained wholesale on commit. It depends on nothing.
package stg

// Change is one staged mutation. Del marks a delete; otherwise it is an
// upsert of Key -> Val.
type Change struct {
	Key string
	Val string
	Del bool
}

// Stage holds staged changes in first-staged order.
type Stage struct {
	ops []Change
	idx map[string]int
}

// New returns an empty Stage.
func New() *Stage { return &Stage{idx: map[string]int{}} }

// Put stages an upsert; re-staging the same key overwrites its value in place.
func (s *Stage) Put(k, v string) {
	if i, ok := s.idx[k]; ok {
		s.ops[i] = Change{Key: k, Val: v}
		return
	}
	s.idx[k] = len(s.ops)
	s.ops = append(s.ops, Change{Key: k, Val: v})
}

// Del stages a delete; re-staging the same key replaces its pending change.
func (s *Stage) Del(k string) {
	if i, ok := s.idx[k]; ok {
		s.ops[i] = Change{Key: k, Del: true}
		return
	}
	s.idx[k] = len(s.ops)
	s.ops = append(s.ops, Change{Key: k, Del: true})
}

// Has reports whether k already has a staged change.
func (s *Stage) Has(k string) bool { _, ok := s.idx[k]; return ok }

// Len returns the number of staged changes.
func (s *Stage) Len() int { return len(s.ops) }

// Take drains all staged changes in order and resets the stage.
func (s *Stage) Take() []Change {
	ops := s.ops
	s.ops, s.idx = nil, map[string]int{}
	return ops
}

// Snapshot returns a copy of the currently staged changes, in order.
func (s *Stage) Snapshot() []Change {
	out := make([]Change, len(s.ops))
	copy(out, s.ops)
	return out
}

// Clear discards all staged changes.
func (s *Stage) Clear() { s.ops, s.idx = nil, map[string]int{} }
