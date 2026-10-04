// Package group tracks placeholder/pending occupancy per concurrency group.
//
// A group has at most one placeholder run (Waiting/Running/Cancelling) and at
// most one pending run at any instant. The table stores only identifiers; all
// lifecycle decisions live in package sched.
package group

// State is the register of one group.
type State struct {
	// Placeholder is the run id currently holding the group slot, or 0.
	Placeholder int64
	// Pending is the run id waiting to become the placeholder, or 0.
	Pending int64
}

func (s *State) empty() bool { return s.Placeholder == 0 && s.Pending == 0 }

// Table maps non-empty group names to their State. Empty group names are not
// registered: runs with an empty group name never collide with anything.
type Table struct {
	groups map[string]*State
}

// NewTable creates an empty group table.
func NewTable() *Table {
	return &Table{groups: make(map[string]*State)}
}

// Lookup returns the group state, or an empty (zero) state when the group is
// absent. The returned pointer is only stable until the next mutating call.
func (t *Table) Lookup(name string) *State {
	if s := t.groups[name]; s != nil {
		return s
	}
	return &State{}
}

// Ensure returns the registered state for name, creating it when absent.
func (t *Table) Ensure(name string) *State {
	s := t.groups[name]
	if s == nil {
		s = &State{}
		t.groups[name] = s
	}
	return s
}

// ClearIfEmpty removes the group entry when it has neither placeholder nor
// pending, keeping the map free of tombstoned groups.
func (t *Table) ClearIfEmpty(name string) {
	if s := t.groups[name]; s != nil && s.empty() {
		delete(t.groups, name)
	}
}

// Groups reports the number of registered groups (used by invariant checks).
func (t *Table) Groups() int { return len(t.groups) }
