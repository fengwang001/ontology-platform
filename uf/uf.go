// Package uf implements a disjoint-set (union-find) data structure.
package uf

import (
	"errors"
	"sync/atomic"
)

// ErrBadIndex is returned by Find, Union and Connected when an element index
// is outside the half-open range [0, n).
var ErrBadIndex = errors.New("uf: element index out of range")

// UF stores disjoint sets in process memory. Fields are unexported.
type UF struct {
	parent []int
	rank   []uint8
	count  int
	hops   atomic.Int32 // edges traversed by the most recent Find, before compression
}

// New creates n singleton sets. New(0) is legal and yields Count()==0.
func New(n int) *UF {
	u := &UF{parent: make([]int, n), rank: make([]uint8, n), count: n}
	for i := range u.parent {
		u.parent[i] = i
	}
	return u
}

// Find returns the representative of the set containing x and records how many
// parent edges it traversed (readable via Hops), applying path compression.
func (u *UF) Find(x int) (int, error) {
	if x < 0 || x >= len(u.parent) {
		return 0, ErrBadIndex
	}
	u.hops.Store(0)
	return u.findRoot(x), nil
}

// findRoot walks to the representative, counting edges and recursively
// repointing every visited node directly at the root (path compression).
func (u *UF) findRoot(x int) int {
	if u.parent[x] == x {
		return x
	}
	u.hops.Add(1)
	root := u.findRoot(u.parent[x])
	if u.parent[x] != root { // skip the write on an already-flat pointer
		u.parent[x] = root
	}
	return root
}

// Union merges the sets containing x and y. It returns true only when two
// previously distinct sets were merged.
func (u *UF) Union(x, y int) (bool, error) {
	rx, err := u.Find(x)
	if err != nil {
		return false, err
	}
	ry, err := u.Find(y)
	if err != nil {
		return false, err
	}
	if rx == ry {
		return false, nil
	}
	switch {
	case u.rank[rx] < u.rank[ry]:
		u.parent[rx] = ry
	case u.rank[rx] > u.rank[ry]:
		u.parent[ry] = rx
	default:
		u.parent[ry] = rx
		u.rank[rx]++
	}
	u.count--
	return true, nil
}

// Connected reports whether x and y belong to the same set.
func (u *UF) Connected(x, y int) (bool, error) {
	rx, err := u.Find(x)
	if err != nil {
		return false, err
	}
	ry, err := u.Find(y)
	if err != nil {
		return false, err
	}
	return rx == ry, nil
}

// Count returns the number of disjoint sets.
func (u *UF) Count() int { return u.count }

// Len returns the number of elements.
func (u *UF) Len() int { return len(u.parent) }

// Hops returns the edge count of the most recent Find (path length to root).
func (u *UF) Hops() int { return int(u.hops.Load()) }
