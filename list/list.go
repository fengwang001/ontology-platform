// Package list 实现确定性跳表本体：查找、插入、删除与每层跨度不变量。
package list

import (
	"errors"
	"fmt"

	"ontology/key"
	"ontology/node"
)

var (
	ErrTooManyElements = errors.New("list: element count limit exceeded")
	ErrLevelLimit      = errors.New("list: key level exceeds configured max level")
	ErrNotFound        = errors.New("list: element not found")
	ErrOutOfRange      = errors.New("list: index out of range")
	ErrBadConfig       = errors.New("list: invalid config")
)

type List struct {
	head                     *node.Node
	maxLevel, maxElems, size int
}

func New(maxLevel, maxElems int) (*List, error) {
	if maxLevel < 1 || maxLevel > key.MaxLevel || maxElems < 1 {
		return nil, ErrBadConfig
	}
	return &List{head: node.Head(maxLevel), maxLevel: maxLevel, maxElems: maxElems}, nil
}

func (l *List) Size() int { return l.size }

func (l *List) find(k key.K, le bool) (update []*node.Node, rank []int, hops int) {
	update, rank = make([]*node.Node, l.maxLevel), make([]int, l.maxLevel)
	x := l.head
	for i := l.maxLevel - 1; i >= 0; i-- {
		rank[i] = rank[min(i+1, l.maxLevel-1)]
		for x.Next[i] != nil && (x.Next[i].Key < k || (le && x.Next[i].Key == k)) {
			rank[i] += x.Span[i]
			x = x.Next[i]
			hops++
		}
		update[i] = x
	}
	return update, rank, hops
}

func (l *List) Insert(k key.K) error {
	if l.size >= l.maxElems {
		return ErrTooManyElements
	}
	lvl := key.Level(k)
	if lvl > l.maxLevel {
		return ErrLevelLimit
	}
	update, rank, _ := l.find(k, false)
	x := node.New(k, lvl)
	for i := 0; i < l.maxLevel; i++ {
		if i < lvl {
			x.Next[i] = update[i].Next[i]
			x.Span[i] = update[i].Span[i] - (rank[0] - rank[i])
			update[i].Next[i] = x
			update[i].Span[i] = rank[0] - rank[i] + 1
		} else {
			update[i].Span[i]++
		}
	}
	l.size++
	return nil
}

func (l *List) Delete(k key.K) error {
	update, _, _ := l.find(k, false)
	x := update[0].Next[0]
	if x == nil || x.Key != k {
		return ErrNotFound
	}
	for i := 0; i < l.maxLevel; i++ {
		if i < x.Level {
			update[i].Span[i] += x.Span[i] - 1
			update[i].Next[i] = x.Next[i]
		} else {
			update[i].Span[i]--
		}
	}
	l.size--
	return nil
}

func (l *List) Bound(k key.K, le bool) (int, int) {
	_, rank, hops := l.find(k, le)
	return rank[0], hops
}

func (l *List) At(idx int) (key.K, int, error) {
	if idx < 0 || idx >= l.size {
		return 0, 0, ErrOutOfRange
	}
	x, pos, hops := l.head, 0, 0
	for i := l.maxLevel - 1; i >= 0; i-- {
		for x.Next[i] != nil && pos+x.Span[i] <= idx {
			pos += x.Span[i]
			x = x.Next[i]
			hops++
		}
	}
	return x.Next[0].Key, hops + 1, nil
}

// SelfCheck 核验：每层跨度精确、每层有序、底层总数与 size 一致。
func (l *List) SelfCheck() error {
	idx := map[*node.Node]int{l.head: -1}
	n := 0
	for x := l.head.Next[0]; x != nil; x = x.Next[0] {
		idx[x], n = n, n+1
	}
	if n != l.size {
		return fmt.Errorf("list: bottom count %d != size %d", n, l.size)
	}
	idx[nil] = n - 1
	for i := 0; i < l.maxLevel; i++ {
		for x := l.head; x != nil; x = x.Next[i] {
			if x.Next[i] != nil && x.Next[i].Key < x.Key {
				return fmt.Errorf("list: level %d out of order", i)
			}
			if x.Span[i] != idx[x.Next[i]]-idx[x] {
				return fmt.Errorf("list: level %d bad span", i)
			}
		}
	}
	return nil
}

// Equal 判定两表结构逐字段相同：层高、每层链接与跨度完全一致。
func Equal(a, b *List) bool {
	if a.maxLevel != b.maxLevel || a.size != b.size {
		return false
	}
	for i := 0; i < a.maxLevel; i++ {
		x, y := a.head, b.head
		for x != nil && y != nil && x.Key == y.Key && x.Span[i] == y.Span[i] {
			x, y = x.Next[i], y.Next[i]
		}
		if x != y {
			return false
		}
	}
	return true
}
