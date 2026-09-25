// Package uf implements a disjoint-set (union-find) over ids 0..n-1
// using union by rank and path compression; safe for concurrent use.
package uf

import (
	"errors"
	"sync"
)

// ErrBadIndex is returned when an element index is out of range.
var ErrBadIndex = errors.New("uf: index out of range")

// UF tracks the connected components of n elements.
type UF struct {
	mu     sync.Mutex
	parent []int
	rank   []int
	count  int
	hops   int // links followed by the most recent find
}

// New returns a UF with n singleton components. n<0 is treated as 0.
func New(n int) *UF {
	if n < 0 {
		n = 0
	}
	parent := make([]int, n)
	for i := range parent {
		parent[i] = i
	}
	return &UF{parent: parent, rank: make([]int, n), count: n}
}

func (u *UF) valid(x int) bool { return 0 <= x && x < len(u.parent) }

// find returns x's root with path compression; caller must hold u.mu.
func (u *UF) find(x int) int {
	u.hops = 0
	root := x
	for u.parent[root] != root {
		root = u.parent[root]
		u.hops++
	}
	for u.parent[x] != root {
		u.parent[x], x = root, u.parent[x]
	}
	return root
}

// Find returns the root of x's component.
func (u *UF) Find(x int) (int, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if !u.valid(x) {
		return -1, ErrBadIndex
	}
	return u.find(x), nil
}

// Union merges the components of x and y by rank, reporting whether
// they were previously disjoint.
func (u *UF) Union(x, y int) (bool, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if !u.valid(x) || !u.valid(y) {
		return false, ErrBadIndex
	}
	rx, ry := u.find(x), u.find(y)
	if rx == ry {
		return false, nil
	}
	if u.rank[rx] < u.rank[ry] {
		rx, ry = ry, rx
	}
	u.parent[ry] = rx
	if u.rank[rx] == u.rank[ry] {
		u.rank[rx]++
	}
	u.count--
	return true, nil
}

// Connected reports whether x and y share a component.
func (u *UF) Connected(x, y int) (bool, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if !u.valid(x) || !u.valid(y) {
		return false, ErrBadIndex
	}
	return u.find(x) == u.find(y), nil
}

// Count returns the current number of connected components.
func (u *UF) Count() int { u.mu.Lock(); defer u.mu.Unlock(); return u.count }

// LastFindHops returns the links followed by the most recent find.
func (u *UF) LastFindHops() int { u.mu.Lock(); defer u.mu.Unlock(); return u.hops }
