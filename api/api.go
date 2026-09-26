// Package api is the public entry point for the two-level perfect hash (FKS).
package api

import (
	"errors"
	"sync"

	"ontology/first"
	"ontology/second"
)

// Sentinel errors: the four rejection causes are pairwise distinct.
var (
	ErrDuplicateKey = errors.New("fks: duplicate key in Build")
	ErrEmptyKeys    = errors.New("fks: empty key set")
	ErrNotFound     = errors.New("fks: key not found")
	ErrInvalidParam = errors.New("fks: invalid parameter")
)

// Structure is an in-memory FKS lookup structure. Reads are safe for
// concurrent use by many goroutines; rejected Builds leave state intact.
type Structure struct {
	mu  sync.RWMutex
	tbl *second.Table
	n   int
}

// New returns an empty structure.
func New() *Structure { return &Structure{} }

func isPrime(p int) bool {
	if p < 2 {
		return false
	}
	for d := 2; d*d <= p; d++ {
		if p%d == 0 {
			return false
		}
	}
	return true
}

// validate returns one of the four sentinel errors, without touching state.
func validate(keys []int, m, p int) error {
	if len(keys) == 0 {
		return ErrEmptyKeys
	}
	seen := make(map[int]bool, len(keys))
	max := keys[0]
	for _, k := range keys {
		if seen[k] {
			return ErrDuplicateKey
		}
		seen[k] = true
		if k > max {
			max = k
		}
	}
	if m < 1 || !isPrime(p) || p <= max {
		return ErrInvalidParam
	}
	return nil
}

// Build constructs the structure. Every rejection (empty set, duplicate key,
// invalid parameter) fails wholesale and never alters previously built state:
// the candidate is built fully off-state and swapped in only on success.
func (s *Structure) Build(keys []int, m, p int) error {
	if err := validate(keys, m, p); err != nil {
		return err
	}
	candidate := second.Build(first.Partition(keys, m), p)
	s.mu.Lock()
	s.tbl, s.n = candidate, len(keys)
	s.mu.Unlock()
	return nil
}

// Lookup reports whether x is a built key. A missing key returns
// (false, ErrNotFound). The stored key is always compared against x, so a
// slot occupied by another key can never be reported as a hit.
func (s *Structure) Lookup(x int) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.tbl == nil {
		return false, ErrNotFound
	}
	if _, ok := s.tbl.Lookup(x); !ok {
		return false, ErrNotFound
	}
	return true, nil
}

// Size reports the number of built keys.
func (s *Structure) Size() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.n
}

// spaceSum independently recomputes Σ n_j² (singleton/empty buckets count 0).
func spaceSum(keys []int, m int) int {
	sum := 0
	for _, b := range first.Partition(keys, m) {
		if len(b) > 1 {
			sum += len(b) * len(b)
		}
	}
	return sum
}

// SelfCheck verifies the four invariants on the built-in key set
// {5,11,13,17,19,24}, m=6, p=29, on a throwaway structure. Returns nil iff
// every check holds (reference match, exact Σ n_j², rejection without trace).
func (s *Structure) SelfCheck() error {
	keys := []int{5, 11, 13, 17, 19, 24}
	tmp := New()
	if err := tmp.Build(keys, 6, 29); err != nil {
		return err
	}
	ref := map[int]bool{}
	for _, k := range keys {
		ref[k] = true
	}
	for x := -30; x <= 60; x++ { // I1 + I2 over a wide residue range
		got, err := tmp.Lookup(x)
		if ref[x] != got || (got == false) != (err == ErrNotFound) {
			return errors.New("fks: selfcheck: reference mismatch")
		}
	}
	if tmp.tbl.SecondSize() != spaceSum(keys, 6) { // I3
		return errors.New("fks: selfcheck: space mismatch")
	}
	// dup key, empty set, m<1, p composite, p<=max — five rejected Builds.
	bad := [][3]any{{[]int{1, 1}, 4, 5}, {[]int(nil), 4, 5}, {[]int{1, 2}, 0, 5}, {[]int{1, 2}, 4, 6}, {[]int{1, 7}, 4, 7}}
	for _, c := range bad {
		if tmp.Build(c[0].([]int), c[1].(int), c[2].(int)) == nil {
			return errors.New("fks: selfcheck: rejection expected")
		}
	}
	if tmp.Size() != len(keys) { // I4: rejection left no trace
		return errors.New("fks: selfcheck: state changed after rejection")
	}
	for _, k := range keys {
		if ok, _ := tmp.Lookup(k); !ok {
			return errors.New("fks: selfcheck: state corrupted after rejection")
		}
	}
	return nil
}
