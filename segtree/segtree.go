// Package segtree 实现支持区间加、区间求和的懒标记线段树。
package segtree

import (
	"errors"
	"fmt"
	"sync"
)

// 哨兵错误：ErrOutOfBounds 与 ErrReversed 均包装 ErrBadRange，可用 errors.Is 区分。
var (
	ErrBadRange    = errors.New("segtree: bad range")
	ErrOutOfBounds = fmt.Errorf("%w: index out of bounds", ErrBadRange)
	ErrReversed    = fmt.Errorf("%w: l > r", ErrBadRange)
)

// Tree 是懒标记线段树，内置互斥锁，可并发使用。
type Tree struct {
	n       int
	sum     []int64
	lazy    []int64 // lazy[i]：欠子节点的 delta，进入子节点前必须 pushdown
	mu      sync.Mutex
	visited int // 单次操作访问的节点数
}

func New(n int) *Tree {
	n = max(n, 1)
	return &Tree{n: n, sum: make([]int64, 4*n), lazy: make([]int64, 4*n)}
}

// LastVisited 返回上一次 AddRange/SumRange 访问的节点数。
func (t *Tree) LastVisited() int { t.mu.Lock(); defer t.mu.Unlock(); return t.visited }

func (t *Tree) op(l, r int, f func()) error {
	if l > r {
		return ErrReversed
	}
	if l < 0 || r >= t.n {
		return ErrOutOfBounds
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.visited = 0
	f()
	return nil
}

// AddRange 把 [l, r] 每个元素加 delta。
func (t *Tree) AddRange(l, r int, delta int64) error {
	return t.op(l, r, func() { t.add(1, 0, t.n-1, l, r, delta) })
}

// SumRange 返回 [l, r] 的和。
func (t *Tree) SumRange(l, r int) (s int64, err error) {
	err = t.op(l, r, func() { s = t.query(1, 0, t.n-1, l, r) })
	return
}
func (t *Tree) apply(i, nl, nr int, d int64) { t.sum[i] += d * int64(nr-nl+1); t.lazy[i] += d }

// pushdown 把节点 i 欠子节点的 delta 传下去并返回区间中点；更新与查询进入子节点前都必须调用。
func (t *Tree) pushdown(i, nl, nr int) int {
	m := (nl + nr) / 2
	if t.lazy[i] != 0 && nl != nr {
		t.apply(2*i, nl, m, t.lazy[i])
		t.apply(2*i+1, m+1, nr, t.lazy[i])
		t.lazy[i] = 0
	}
	return m
}

func (t *Tree) add(i, nl, nr, l, r int, d int64) {
	t.visited++
	if l <= nl && nr <= r {
		t.apply(i, nl, nr, d)
		return
	}
	m := t.pushdown(i, nl, nr)
	if l <= m {
		t.add(2*i, nl, m, l, r, d)
	}
	if r > m {
		t.add(2*i+1, m+1, nr, l, r, d)
	}
	t.sum[i] = t.sum[2*i] + t.sum[2*i+1]
}

func (t *Tree) query(i, nl, nr, l, r int) (s int64) {
	t.visited++
	if l <= nl && nr <= r {
		return t.sum[i]
	}
	m := t.pushdown(i, nl, nr) // 关键：查询进入子节点前必须下推，否则读到陈旧值
	if l <= m {
		s += t.query(2*i, nl, m, l, r)
	}
	if r > m {
		s += t.query(2*i+1, m+1, nr, l, r)
	}
	return s
}
