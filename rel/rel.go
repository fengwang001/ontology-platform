// Package rel is a single-relation multiset: tuple counts with one-at-a-time insert/delete.
package rel

import "errors"

// ErrNotFound is returned when deleting a tuple whose current count is 0.
var ErrNotFound = errors.New("rel: tuple not found")

// Tuple is one ordered pair of integer fields.
type Tuple struct{ X, Y int }

// Rel is a multiset of tuples backed by in-process memory.
// It is not internally synchronized; join.DB serializes all access with one mutex.
type Rel struct {
	m map[Tuple]int
}

// New returns an empty relation.
func New() *Rel { return &Rel{m: make(map[Tuple]int)} }

// Insert adds exactly one copy of t (count +1).
func (r *Rel) Insert(t Tuple) {
	r.m[t]++
}

// Delete removes exactly one copy of t (count -1).
// It returns ErrNotFound when the current count of t is 0; state is untouched.
func (r *Rel) Delete(t Tuple) error {
	if r.m[t] == 0 {
		return ErrNotFound
	}
	if r.m[t] == 1 {
		delete(r.m, t)
	} else {
		r.m[t]--
	}
	return nil
}

// Count reports how many copies of t are currently present.
func (r *Rel) Count(t Tuple) int { return r.m[t] }

// Len reports the total number of tuple copies in the relation.
func (r *Rel) Len() int {
	n := 0
	for _, c := range r.m {
		n += c
	}
	return n
}

// All returns a snapshot copy of tuple -> count, safe for the caller to mutate.
func (r *Rel) All() map[Tuple]int {
	out := make(map[Tuple]int, len(r.m))
	for t, c := range r.m {
		out[t] = c
	}
	return out
}
