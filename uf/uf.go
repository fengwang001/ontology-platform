// Package uf implements a union-find (disjoint set union) structure with
// union by rank and path compression. State lives in process memory.
package uf

import (
	"errors"
	"sync"
)

var (
	// ErrNegativeSize is returned by New for a negative element count.
	ErrNegativeSize = errors.New("uf: negative size")
	// ErrBadIndex is returned for an element index outside [0, n).
	ErrBadIndex = errors.New("uf: index out of range")
	// ErrNilReceiver is returned when a method is called on a nil set.
	ErrNilReceiver = errors.New("uf: nil receiver")
)

// DSU is a concurrency-safe union-find set over n fixed elements.
type DSU struct {
	mu     sync.Mutex
	parent []int
	rank   []uint8
	count  int
	hops   int
}

// New creates a set of n singleton elements. New(0) is valid.
func New(n int) (*DSU, error) {
	if n < 0 {
		return nil, ErrNegativeSize
	}
	set := &DSU{parent: make([]int, n), rank: make([]uint8, n), count: n}
	for index := range set.parent {
		set.parent[index] = index
	}
	return set, nil
}

func (set *DSU) find(x int) int {
	root := x
	hops := 0
	for set.parent[root] != root {
		root = set.parent[root]
		hops++
	}
	for set.parent[x] != x {
		next := set.parent[x]
		set.parent[x] = root
		x = next
	}
	set.hops = hops
	return root
}

// Find returns the root of x and records the pre-compression hop count.
func (set *DSU) Find(x int) (int, error) {
	if set == nil {
		return 0, ErrNilReceiver
	}
	set.mu.Lock()
	defer set.mu.Unlock()
	if x < 0 || x >= len(set.parent) {
		return 0, ErrBadIndex
	}
	return set.find(x), nil
}

// Union merges the components of x and y, reporting whether two distinct
// components were merged.
func (set *DSU) Union(x, y int) (bool, error) {
	if set == nil {
		return false, ErrNilReceiver
	}
	set.mu.Lock()
	defer set.mu.Unlock()
	if x < 0 || x >= len(set.parent) || y < 0 || y >= len(set.parent) {
		return false, ErrBadIndex
	}
	rx, ry := set.find(x), set.find(y)
	if rx == ry {
		return false, nil
	}
	switch {
	case set.rank[rx] < set.rank[ry]:
		rx, ry = ry, rx
	case set.rank[rx] == set.rank[ry]:
		set.rank[rx]++
	}
	set.parent[ry] = rx
	set.count--
	return true, nil
}

// Connected reports whether x and y belong to the same component.
func (set *DSU) Connected(x, y int) (bool, error) {
	if set == nil {
		return false, ErrNilReceiver
	}
	set.mu.Lock()
	defer set.mu.Unlock()
	if x < 0 || x >= len(set.parent) || y < 0 || y >= len(set.parent) {
		return false, ErrBadIndex
	}
	return set.find(x) == set.find(y), nil
}

// Count returns the current number of components.
func (set *DSU) Count() int {
	if set == nil {
		return 0
	}
	set.mu.Lock()
	defer set.mu.Unlock()
	return set.count
}

// LastFindHops returns the pre-compression parent hops of the most recent Find.
func (set *DSU) LastFindHops() int {
	set.mu.Lock()
	defer set.mu.Unlock()
	return set.hops
}
