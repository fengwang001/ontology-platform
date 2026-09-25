// Package casc keeps the entity set with a child adjacency list and
// implements cascading delete (post-order) plus orphan listing/cleanup.
package casc

import (
	"errors"
	"slices"
	"sync"

	"ontology/ent"
)

// ErrNotFound is returned when deleting an id that does not exist.
var ErrNotFound = errors.New("casc: entity not found")

// Set is a concurrency-safe entity set with a child adjacency list.
type Set struct {
	mu       sync.RWMutex
	ents     map[int]ent.Entity
	children map[int][]int // parent -> child ids, ascending
	// lastTraversed counts entities visited while computing the most
	// recent Delete set. Unexported on purpose: no exported API reads it.
	lastTraversed int
}

func New() *Set {
	return &Set{ents: map[int]ent.Entity{}, children: map[int][]int{}}
}

func (s *Set) Exists(id int) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.existsLocked(id)
}

// Add inserts a fully validated entity; any rejection leaves state intact.
func (s *Set) Add(id, parent int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := ent.Entity{ID: id, Parent: parent}
	if err := e.Validate(s.existsLocked); err != nil {
		return err
	}
	s.insertLocked(e)
	return nil
}

// Insert seeds an entity without requiring the parent to exist, so tests
// and demos can construct orphans. It still rejects bad or duplicate ids.
func (s *Set) Insert(id, parent int) error {
	if id < 1 {
		return ent.ErrInvalidID
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.existsLocked(id) {
		return ent.ErrDuplicateID
	}
	s.insertLocked(ent.Entity{ID: id, Parent: parent})
	return nil
}

// Delete removes root and all descendants, returning the delete set in
// post-order (children before parent, siblings ascending).
func (s *Set) Delete(root int) ([]int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.existsLocked(root) {
		return nil, ErrNotFound
	}
	var set []int
	n := 0
	s.postorderLocked(root, &set, &n)
	s.lastTraversed = n
	s.eraseLocked(set)
	return set, nil
}

// Orphans lists ids whose Parent != 0 but is absent, ascending.
func (s *Set) Orphans() []int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.orphansLocked()
}

// CleanupOrphans deletes every orphan and its descendants (orphanhood
// cascades), returning the combined delete set in post-order.
func (s *Set) CleanupOrphans() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []int
	for _, id := range s.orphansLocked() {
		s.postorderLocked(id, &out, nil)
	}
	s.eraseLocked(out)
	return out
}

func (s *Set) existsLocked(id int) bool {
	_, ok := s.ents[id]
	return ok
}

func (s *Set) insertLocked(e ent.Entity) {
	s.ents[e.ID] = e
	if e.Parent != 0 {
		c := s.children[e.Parent]
		i, _ := slices.BinarySearch(c, e.ID)
		s.children[e.Parent] = slices.Insert(c, i, e.ID)
	}
}

// postorderLocked appends the subtree rooted at id in post-order; siblings
// are ascending because children buckets are kept sorted. cnt, when non-nil,
// counts visited entities.
func (s *Set) postorderLocked(id int, out *[]int, cnt *int) {
	if cnt != nil {
		*cnt++
	}
	for _, c := range s.children[id] {
		s.postorderLocked(c, out, cnt)
	}
	*out = append(*out, id)
}

// orphansLocked must be called with the lock held; result is ascending.
func (s *Set) orphansLocked() []int {
	var out []int
	for id, e := range s.ents {
		if e.Orphan(s.existsLocked) {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return out
}

// eraseLocked removes ids and detaches each from its parent's bucket.
func (s *Set) eraseLocked(ids []int) {
	for _, id := range ids {
		if p := s.ents[id].Parent; p != 0 {
			c := s.children[p]
			if i, ok := slices.BinarySearch(c, id); ok {
				s.children[p] = slices.Delete(c, i, i+1)
			}
		}
		delete(s.children, id)
		delete(s.ents, id)
	}
}
