// Package sparsebitset implements an adaptive sparse bit set over uint32
// values. Elements are partitioned into containers by their high 16 bits;
// each container is stored either as a sorted array (cardinality <= 4096)
// or as a 1024-word bitmap (cardinality > 4096).
package sparsebitset

import (
	"errors"
	"sync"
)

const (
	// arrayMaxCardinality is the maximum cardinality of an array container.
	// A container with exactly this many elements is still an array.
	arrayMaxCardinality = 4096
	// bitmapWords is the number of uint64 words in a bitmap container.
	bitmapWords = 1024
	// maxHi is the exclusive upper bound for AddRange's hi argument.
	maxHi = uint64(1) << 32
)

var (
	// ErrLoGreaterThanHi is returned by AddRange when lo > hi.
	ErrLoGreaterThanHi = errors.New("sparsebitset: lo is greater than hi")
	// ErrHiTooLarge is returned by AddRange when hi > 2^32.
	ErrHiTooLarge = errors.New("sparsebitset: hi exceeds 2^32")
	// ErrSelectOutOfRange is returned by Select when k is out of range.
	ErrSelectOutOfRange = errors.New("sparsebitset: select index out of range")
)

// Set is a sparse bit set of uint32 values safe for concurrent use.
type Set struct {
	mu         sync.RWMutex
	keys       []uint16
	containers []*container
}

// New returns an empty Set.
func New() *Set {
	return &Set{}
}

// Add inserts x into the set.
func (s *Set) Add(x uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.add(x)
}

// Remove deletes x from the set; removing an absent element is a no-op.
func (s *Set) Remove(x uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.remove(x)
}

// Contains reports whether x is in the set.
func (s *Set) Contains(x uint32) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.contains(x)
}

// Cardinality returns the number of elements in the set.
func (s *Set) Cardinality() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cardinality()
}

// And returns a new set containing the intersection of s and other.
// Each result container is normalized by its result cardinality.
func (s *Set) And(other *Set) *Set {
	first, second := s, other
	if second != s && lessSet(second, first) {
		first, second = second, first
	}
	first.mu.RLock()
	defer first.mu.RUnlock()
	if second != first {
		second.mu.RLock()
		defer second.mu.RUnlock()
	}
	return s.and(other)
}

// Rank returns the number of elements in the set that are <= x.
func (s *Set) Rank(x uint32) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.rank(x)
}

// Select returns the k-th smallest element (0-based).
func (s *Set) Select(k int) (uint32, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.selectK(k)
}

// AddRange inserts every value in the half-open interval [lo, hi).
func (s *Set) AddRange(lo, hi uint64) error {
	if lo > hi {
		return ErrLoGreaterThanHi
	}
	if hi > maxHi {
		return ErrHiTooLarge
	}
	if lo == hi {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.addRange(lo, hi)
	return nil
}

// Stats returns the number of array containers and bitmap containers.
func (s *Set) Stats() (arrays, bitmaps int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, c := range s.containers {
		if c.isBitmap() {
			bitmaps++
		} else {
			arrays++
		}
	}
	return arrays, bitmaps
}

func lessSet(a, b *Set) bool {
	return uintptrOf(a) < uintptrOf(b)
}
