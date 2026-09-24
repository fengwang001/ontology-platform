// Package undo defines undo records, savepoint markers and the active-set
// membership test. It depends on no other package.
package undo

import "sort"

// Record captures everything needed to undo one Set call.
type Record struct {
	Key     string
	Old     int
	Existed bool
}

// Marker tags a savepoint inside the store's undo stream.
type Marker struct {
	ID int
}

// Active is the set of currently live savepoint ids.
type Active struct {
	live map[int]bool
}

// NewActive returns an empty active set.
func NewActive() *Active {
	return &Active{live: map[int]bool{}}
}

// Add marks id as live.
func (a *Active) Add(id int) {
	a.live[id] = true
}

// Has reports whether id is live.
func (a *Active) Has(id int) bool {
	return a.live[id]
}

// Drop deactivates id and every deeper (larger) id. It reports whether id
// itself was live; the set is left untouched when it was not, so a rejected
// Release/Rollback leaves no trace.
func (a *Active) Drop(id int) bool {
	if !a.live[id] {
		return false
	}
	for k := range a.live {
		if k >= id {
			delete(a.live, k)
		}
	}
	return true
}

// IDs returns the live ids in ascending order.
func (a *Active) IDs() []int {
	ids := make([]int, 0, len(a.live))
	for k := range a.live {
		ids = append(ids, k)
	}
	sort.Ints(ids)
	return ids
}
