// Package skip 实现可复现的有序跳表：层级由 seed 派生的伪随机流决定。
package skip

import (
	"errors"
	"math/rand/v2"
	"sync/atomic"

	"ontology/node"
)

var ErrBadRange = errors.New("skip: lo >= hi")
var ErrDuplicate = errors.New("skip: duplicate key")
var ErrNotFound = errors.New("skip: key not found")

type List[T any] struct {
	head   *node.Node[T]
	length int
	rng    *rand.Rand
	cmp    atomic.Int64
}

// New 以 seed 创建空跳表；同 seed 同插入序列必得同一结构。
func New[T any](seed uint64) *List[T] {
	return &List[T]{head: node.New(0, *new(T), node.MaxLevel),
		rng: rand.New(rand.NewPCG(seed, seed^0x9E3779B97F4A7C15))}
}

func (l *List[T]) seek(key int, upd *[node.MaxLevel]*node.Node[T], count bool) *node.Node[T] {
	cur := l.head
	for i := node.MaxLevel - 1; i >= 0; i-- {
		for n := cur.Next[i]; n != nil && n.Key < key; n = cur.Next[i] {
			if count {
				l.cmp.Add(1)
			}
			cur = n
		}
		upd[i] = cur
	}
	return cur.Next[0]
}

// Insert 插入 key/val，重复 key 返回 ErrDuplicate（不消耗随机流）。
func (l *List[T]) Insert(key int, val T) error {
	var upd [node.MaxLevel]*node.Node[T]
	if n := l.seek(key, &upd, false); n != nil && n.Key == key {
		return ErrDuplicate
	}
	lvl := 1
	for lvl < node.MaxLevel && l.rng.Uint64()&1 == 1 {
		lvl++
	}
	n := node.New(key, val, lvl)
	for i := 0; i < lvl; i++ {
		n.Next[i], upd[i].Next[i] = upd[i].Next[i], n
	}
	l.length++
	return nil
}

func (l *List[T]) Find(key int) (T, bool) {
	if n := l.seek(key, new([node.MaxLevel]*node.Node[T]), true); n != nil && n.Key == key {
		return n.Val, true
	}
	return *new(T), false
}

func (l *List[T]) Delete(key int) error {
	var upd [node.MaxLevel]*node.Node[T]
	n := l.seek(key, &upd, false)
	if n == nil || n.Key != key {
		return ErrNotFound
	}
	for i := range n.Next {
		upd[i].Next[i] = n.Next[i]
	}
	l.length--
	return nil
}

func (l *List[T]) Range(lo, hi int) (out []T, err error) {
	if lo >= hi {
		return nil, ErrBadRange
	}
	for n := l.seek(lo, new([node.MaxLevel]*node.Node[T]), false); n != nil && n.Key < hi; n = n.Next[0] {
		out = append(out, n.Val)
	}
	return out, nil
}

func (l *List[T]) Len() int { return l.length }

func (l *List[T]) Compares() int64 { return l.cmp.Load() }

func (l *List[T]) Levels() (out []int) {
	for n := l.head.Next[0]; n != nil; n = n.Next[0] {
		out = append(out, len(n.Next))
	}
	return
}
