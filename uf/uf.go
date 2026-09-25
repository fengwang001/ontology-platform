package uf

import (
	"errors"
	"sync"
)

var (
	ErrBadIndex     = errors.New("uf: index out of range")
	ErrNegativeSize = errors.New("uf: negative size")
	ErrEmptySet     = errors.New("uf: empty set")
)

type DSU struct {
	parent      []int
	rank        []uint8
	count, hops int // hops：最近一次 Find 的父指针跳数（非导出计数器）
	mu          sync.RWMutex
}

// New 创建含 n 个独立元素的并查集；n==0 合法。
func New(n int) (*DSU, error) {
	if n < 0 {
		return nil, ErrNegativeSize
	}
	d := &DSU{parent: make([]int, n), rank: make([]uint8, n), count: n}
	for i := 0; i < n; i++ {
		d.parent[i] = i
	}
	return d, nil
}

// Find 返回 x 所在分量的根并做路径压缩。
func (d *DSU) Find(x int) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.findLocked(x)
}

func (d *DSU) findLocked(x int) (int, error) {
	d.hops = 0
	if len(d.parent) == 0 {
		return 0, ErrEmptySet
	}
	if x < 0 || x >= len(d.parent) {
		return 0, ErrBadIndex
	}
	for d.parent[x] != x {
		d.parent[x] = d.parent[d.parent[x]] // 路径减半
		x, d.hops = d.parent[x], d.hops+1
	}
	return x, nil
}

// Union 真正合并两个不同分量时返回 true。
func (d *DSU) Union(x, y int) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	rx, ry, err := d.rootsLocked(x, y)
	if err != nil {
		return false, err
	}
	if rx == ry {
		return false, nil
	}
	if d.rank[rx] < d.rank[ry] { // 按秩合并：小树挂大树
		rx, ry = ry, rx
	}
	d.parent[ry] = rx
	if d.rank[rx] == d.rank[ry] {
		d.rank[rx]++
	}
	d.count--
	return true, nil
}

// Connected 判断 x、y 是否在同一分量。
func (d *DSU) Connected(x, y int) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	rx, ry, err := d.rootsLocked(x, y)
	if err != nil {
		return false, err
	}
	return rx == ry, nil
}

func (d *DSU) rootsLocked(x, y int) (int, int, error) {
	rx, ex := d.findLocked(x)
	ry, ey := d.findLocked(y)
	if ex != nil {
		return 0, 0, ex
	}
	return rx, ry, ey
}

func (d *DSU) snapshot() (count, hops int) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.count, d.hops
}

// Count 返回当前连通分量数。
func (d *DSU) Count() int { c, _ := d.snapshot(); return c }

// LastFindHops 暴露非导出计数器：最近一次 Find 的跳数。
func (d *DSU) LastFindHops() int { _, h := d.snapshot(); return h }
