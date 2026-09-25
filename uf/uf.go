package uf

import (
	"errors"
	"sync"
)

var (
	ErrBadIndex     = errors.New("uf: element index out of range")
	ErrNegativeSize = errors.New("uf: size must not be negative")
)

type DSU struct {
	mu     sync.RWMutex
	parent []int
	rank   []uint8
	count  int
	hops   int
}

func New(n int) (*DSU, error) {
	if n < 0 {
		return nil, ErrNegativeSize
	}
	d := &DSU{parent: make([]int, n), rank: make([]uint8, n), count: n}
	for i := range d.parent {
		d.parent[i] = i // every element starts as its own component
	}
	return d, nil
}

func (d *DSU) Find(x int) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.valid(x) {
		return 0, ErrBadIndex
	}
	return d.findLocked(x), nil
}

func (d *DSU) valid(x int) bool { return 0 <= x && x < len(d.parent) }
func (d *DSU) findLocked(x int) int {
	d.hops = 0
	root := x
	for d.parent[root] != root {
		root = d.parent[root]
		d.hops++
	}
	for d.parent[x] != x {
		next := d.parent[x]
		d.parent[x] = root
		x = next
	}
	return root
}

func (d *DSU) Union(x, y int) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.valid(x) || !d.valid(y) {
		return false, ErrBadIndex
	}
	rx, ry := d.findLocked(x), d.findLocked(y)
	if rx == ry {
		return false, nil
	}
	switch {
	case d.rank[rx] < d.rank[ry]:
		d.parent[rx] = ry
	case d.rank[rx] > d.rank[ry]:
		d.parent[ry] = rx
	default:
		d.parent[ry] = rx
		d.rank[rx]++
	}
	d.count--
	return true, nil
}

func (d *DSU) Connected(x, y int) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.valid(x) || !d.valid(y) {
		return false, ErrBadIndex
	}
	return d.findLocked(x) == d.findLocked(y), nil
}

func (d *DSU) Count() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.count
}

func (d *DSU) LastFindHops() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.hops
}
