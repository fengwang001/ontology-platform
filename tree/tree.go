// Package tree 提供以区间端点为键的 AVL 有序树，并维护子树最大右端点。
// 依赖 node 与 ival；不依赖 query。
package tree

import (
	"sync"

	"ontology/ival"
	"ontology/node"
)

// DefaultMaxIntervals 是零值 Tree 的默认容量上限。
const DefaultMaxIntervals = 1_000_000

// Tree 是进程内的区间 AVL 树。零值不可用，请用 New。
type Tree struct {
	mu         sync.RWMutex
	root       *node.Node
	count      int
	maxEntries int
}

// Option 配置 Tree。
type Option func(*Tree)

// WithMaxIntervals 设置区间总数上限；n<=0 表示不限制。
func WithMaxIntervals(n int) Option {
	return func(t *Tree) { t.maxEntries = n }
}

// New 创建空树。
func New(opts ...Option) *Tree {
	t := &Tree{maxEntries: DefaultMaxIntervals}
	for _, o := range opts {
		o(t)
	}
	return t
}

// cmp 以 (L,R) 为键。完全相同的区间键相等，作为多重集共存。
func cmp(a, b ival.Interval) int {
	switch {
	case a.L < b.L:
		return -1
	case a.L > b.L:
		return 1
	case a.R < b.R:
		return -1
	case a.R > b.R:
		return 1
	default:
		return 0
	}
}

// Insert 插入一个区间。非法区间或超限时返回错误且不改变任何状态。
func (t *Tree) Insert(iv ival.Interval) error {
	if iv.L > iv.R {
		return ival.ErrInvalidInterval
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.maxEntries > 0 && t.count >= t.maxEntries {
		return &LimitError{Limit: t.maxEntries}
	}
	t.root = insert(t.root, iv)
	t.count++
	return nil
}

func insert(n *node.Node, iv ival.Interval) *node.Node {
	if n == nil {
		return node.New(iv)
	}
	if cmp(iv, n.Iv) < 0 {
		n.Left = insert(n.Left, iv)
	} else {
		n.Right = insert(n.Right, iv)
	}
	return node.Rebalance(n)
}

// Delete 删除多重集里该区间的一个副本（中序第一个）。
// 不存在时返回 ErrNotFound，且不改变状态。
func (t *Tree) Delete(iv ival.Interval) error {
	if iv.L > iv.R {
		return ival.ErrInvalidInterval
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if !contains(t.root, iv) {
		return ErrNotFound
	}
	t.root = deleteOne(t.root, iv)
	t.count--
	return nil
}

func contains(n *node.Node, iv ival.Interval) bool {
	for n != nil {
		c := cmp(iv, n.Iv)
		switch {
		case c < 0:
			n = n.Left
		case c > 0:
			n = n.Right
		default:
			return true
		}
	}
	return false
}

func deleteOne(n *node.Node, iv ival.Interval) *node.Node {
	c := cmp(iv, n.Iv)
	switch {
	case c < 0:
		n.Left = deleteOne(n.Left, iv)
	case c > 0:
		n.Right = deleteOne(n.Right, iv)
	default:
		switch {
		case n.Left == nil:
			return n.Right
		case n.Right == nil:
			return n.Left
		default:
			succ := minNode(n.Right)
			n.Iv = succ.Iv
			n.Right = deleteOne(n.Right, succ.Iv)
		}
	}
	return node.Rebalance(n)
}

func minNode(n *node.Node) *node.Node {
	for n.Left != nil {
		n = n.Left
	}
	return n
}

// Len 返回已插入区间数（多重集计数）。
func (t *Tree) Len() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.count
}
