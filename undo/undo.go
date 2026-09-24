// Package undo holds the undo-record shape, savepoint markers and the active
// savepoint set. It depends on no other package.
package undo

// Record reverses exactly one Set: it restores Key to the state
// (Exist, Value) it had immediately before that Set.
type Record struct {
	Key   string
	Exist bool
	Value int
}

// Marker is a savepoint mark pushed onto the undo log. A marker left in the
// log after Release is marked Dead: it delimits nothing anymore.
type Marker struct {
	ID   int
	Dead bool
}

// Active is the set of savepoint ids that may still be rolled back to or
// released. Ids are allocated monotonically, so a deeper savepoint always
// has a strictly larger id.
type Active struct {
	m map[int]struct{}
}

// NewActive returns an empty active set.
func NewActive() *Active {
	return &Active{m: make(map[int]struct{})}
}

// Add inserts id into the active set.
func (a *Active) Add(id int) { a.m[id] = struct{}{} }

// Has reports whether id is active.
func (a *Active) Has(id int) bool {
	_, ok := a.m[id]
	return ok
}

// Drop invalidates id and every deeper (larger-id) active savepoint.
func (a *Active) Drop(id int) {
	for x := range a.m {
		if x >= id {
			delete(a.m, x)
		}
	}
}
