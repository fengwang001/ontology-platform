// Package list 实现确定性跳表本体：插入、删除与跨度不变量维护。
package list

import (
	"errors"
	"sync/atomic"

	"ontology/key"
	"ontology/node"
)

var (
	ErrNotFound   = errors.New("list: 元素不存在")
	ErrTooMany    = errors.New("list: 元素总数超限")
	ErrLevelLimit = errors.New("list: 层高超限")
	ErrCorrupt    = errors.New("list: 结构自检失败")
)

// List 是确定性索引跳表。head 为哨兵，各层 Span 始终精确。
type List struct {
	head     *node.Node
	size     int
	maxLvl   int
	maxElems int
	vers     atomic.Uint64
}

// New 创建空表；maxLevel 为最大层数上限，maxElements 为元素总数上限。
func New(maxLevel, maxElements int) *List {
	return &List{head: node.New(0, maxLevel), maxLvl: maxLevel, maxElems: maxElements}
}

func (l *List) Header() *node.Node { return l.head }
func (l *List) MaxLevel() int      { return l.maxLvl }
func (l *List) Size() int          { return l.size }
func (l *List) Version() uint64    { return l.vers.Load() }

// Insert 插入一个元素（允许重复）。超限在任何修改之前拒绝。
func (l *List) Insert(k key.Key) error {
	lvl := k.Level()
	if lvl > l.maxLvl {
		return ErrLevelLimit
	}
	if l.size >= l.maxElems {
		return ErrTooMany
	}
	update := make([]*node.Node, l.maxLvl)
	rank := make([]int, l.maxLvl)
	x := l.head
	for i := l.maxLvl - 1; i >= 0; i-- {
		if i == l.maxLvl-1 {
			rank[i] = 0
		} else {
			rank[i] = rank[i+1]
		}
		for x.Next[i] != nil && key.Compare(x.Next[i].Key, k) < 0 {
			rank[i] += x.Span[i]
			x = x.Next[i]
		}
		update[i] = x
	}
	n := node.New(k, lvl)
	for i := 0; i < lvl; i++ {
		n.Next[i] = update[i].Next[i]
		n.Span[i] = update[i].Span[i] - (rank[0] - rank[i])
		update[i].Next[i] = n
		update[i].Span[i] = rank[0] - rank[i] + 1
	}
	for i := lvl; i < l.maxLvl; i++ {
		update[i].Span[i]++
	}
	l.size++
	l.vers.Add(1)
	return nil
}

// Delete 删除一个等于 k 的元素；不存在返回 ErrNotFound。
func (l *List) Delete(k key.Key) error {
	update := make([]*node.Node, l.maxLvl)
	x := l.head
	for i := l.maxLvl - 1; i >= 0; i-- {
		for x.Next[i] != nil && key.Compare(x.Next[i].Key, k) < 0 {
			x = x.Next[i]
		}
		update[i] = x
	}
	x = x.Next[0]
	if x == nil || !key.Equal(x.Key, k) {
		return ErrNotFound
	}
	for i := 0; i < l.maxLvl; i++ {
		if i < x.Level() && update[i].Next[i] == x {
			update[i].Span[i] += x.Span[i] - 1 // 被删节点跨度并入前驱
			update[i].Next[i] = x.Next[i]
		} else {
			update[i].Span[i]--
		}
	}
	l.size--
	l.vers.Add(1)
	return nil
}

// Check 自检：每层跨度精确、每层有序、底层总数与规模一致。
func (l *List) Check() error {
	n := 0
	for x := l.head.Next[0]; x != nil; x = x.Next[0] {
		n++
	}
	if n != l.size {
		return ErrCorrupt
	}
	for i := 0; i < l.maxLvl; i++ {
		x := l.head
		for {
			steps := 0
			for y := x; y != x.Next[i]; y = y.Next[0] {
				steps++
			}
			if x.Next[i] == nil {
				steps-- // nil 指针跨度只计剩余元素，不计最后一条到 nil 的边
			}
			if steps != x.Span[i] {
				return ErrCorrupt
			}
			if x.Next[i] == nil {
				break
			}
			if x != l.head && key.Compare(x.Next[i].Key, x.Key) < 0 {
				return ErrCorrupt
			}
			x = x.Next[i]
		}
	}
	return nil
}

// Equal 判定两表结构逐字段相同：元素键、层高、每层链接与跨度。
func (l *List) Equal(o *List) bool {
	if l.size != o.size || l.maxLvl != o.maxLvl {
		return false
	}
	a, b := l.head.Next[0], o.head.Next[0]
	for a != nil && b != nil {
		if a.Key != b.Key || a.Level() != b.Level() {
			return false
		}
		a, b = a.Next[0], b.Next[0]
	}
	if a != nil || b != nil {
		return false
	}
	for i := 0; i < l.maxLvl; i++ {
		pa, pb := l.head, o.head
		for {
			if pa.Span[i] != pb.Span[i] || (pa.Next[i] == nil) != (pb.Next[i] == nil) {
				return false
			}
			if pa.Next[i] == nil {
				break
			}
			pa, pb = pa.Next[i], pb.Next[i]
		}
	}
	return true
}
