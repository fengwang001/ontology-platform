package uf

import (
	"errors"
	"sync"
)

var (
	ErrBadIndex = errors.New("uf: index out of range")
	ErrBadSize  = errors.New("uf: negative size")
	ErrTooLarge = errors.New("uf: size too large")
)

const maxInt = int(^uint(0) >> 1)

type DSU struct {
	mu     sync.RWMutex
	parent []int
	rank   []uint8
	count  int
	hops   int
}

func New(n int) (*DSU, error) {
	if n < 0 {
		return nil, ErrBadSize
	}
	if uint64(n) > uint64(maxInt) {
		return nil, ErrTooLarge
	}
	parent := make([]int, n)
	for i := range parent {
		parent[i] = i
	}
	return &DSU{parent: parent, rank: make([]uint8, n), count: n}, nil
}

func (d *DSU) Find(x int) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if x < 0 || x >= len(d.parent) {
		d.hops = 0
		return 0, ErrBadIndex
	}
	return d.findLocked(x), nil
}

func (d *DSU) Union(x, y int) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if x < 0 || x >= len(d.parent) || y < 0 || y >= len(d.parent) {
		d.hops = 0
		return false, ErrBadIndex
	}
	rootX, rootY := d.findLocked(x), d.findLocked(y)
	if rootX == rootY {
		return false, nil
	}
	if d.rank[rootX] < d.rank[rootY] {
		rootX, rootY = rootY, rootX
	}
	d.parent[rootY] = rootX
	if d.rank[rootX] == d.rank[rootY] {
		d.rank[rootX]++
	}
	d.count--
	return true, nil
}

func (d *DSU) Connected(x, y int) (bool, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if x < 0 || x >= len(d.parent) || y < 0 || y >= len(d.parent) {
		return false, ErrBadIndex
	}
	rootX, rootY := x, y
	for d.parent[rootX] != rootX {
		rootX = d.parent[rootX]
	}
	for d.parent[rootY] != rootY {
		rootY = d.parent[rootY]
	}
	return rootX == rootY, nil
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

func (d *DSU) findLocked(x int) int {
	if x < 0 || x >= len(d.parent) {
		d.hops = 0
		return -1
	}
	root, hops := x, 0
	for d.parent[root] != root {
		root = d.parent[root]
		hops++
	}
	for d.parent[x] != x {
		next := d.parent[x]
		d.parent[x] = root
		x = next
	}
	d.hops = hops
	return root
}
