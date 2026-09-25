// Package uf 提供带路径压缩与按秩合并的并查集，状态保存在进程内存。
package uf

import (
	"errors"
	"sync"
)

// ErrBadIndex 在元素下标超出 [0,n) 范围时返回（含负数）。
var ErrBadIndex = errors.New("uf: index out of range")

// ErrNegativeSize 在 New 收到负容量时返回。
var ErrNegativeSize = errors.New("uf: size must not be negative")

// UnionFind 维护 n 个元素的动态连通分量。
type UnionFind struct {
	mu      sync.Mutex
	parent  []int
	rank    []int
	count   int
	lastHop int // 最近一次 Find 上溯经过的父指针跳数（非导出计数器）
}

// New 创建 0..n-1 各自独立的并查集；n==0 合法。
func New(n int) (*UnionFind, error) {
	if n < 0 {
		return nil, ErrNegativeSize
	}
	u := &UnionFind{parent: make([]int, n), rank: make([]int, n), count: n}
	for i := range u.parent {
		u.parent[i] = i
	}
	return u, nil
}

// find 返回根并做路径压缩，记录沿途跳数。调用方需持有 u.mu。
func (u *UnionFind) find(x int) int {
	hop := 0
	root := x
	for u.parent[root] != root {
		root = u.parent[root]
		hop++
	}
	for u.parent[x] != x {
		next := u.parent[x]
		u.parent[x] = root
		x = next
}
	u.lastHop = hop
	return root
}

// Find 返回 x 所在分量的根；越界返回 ErrBadIndex。
func (u *UnionFind) Find(x int) (int, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if x < 0 || x >= len(u.parent) {
		return 0, ErrBadIndex
	}
	return u.find(x), nil
}

// Union 合并 x、y 所在分量；原本不同分量返回 true，越界返回 ErrBadIndex。
func (u *UnionFind) Union(x, y int) (bool, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if x < 0 || x >= len(u.parent) || y < 0 || y >= len(u.parent) {
		return false, ErrBadIndex
	}
	rx, ry := u.find(x), u.find(y)
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

// Connected 报告 x、y 是否在同一分量；越界返回 ErrBadIndex。
func (u *UnionFind) Connected(x, y int) (bool, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if x < 0 || x >= len(u.parent) || y < 0 || y >= len(u.parent) {
		return false, ErrBadIndex
	}
	return u.find(x) == u.find(y), nil
}

// Count 返回当前连通分量数。
func (u *UnionFind) Count() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.count
}

// LastHop 返回最近一次内部 Find 的父指针跳数，仅供复杂度测试钉住。
func (u *UnionFind) LastHop() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.lastHop
}
