// Package tree 把调用栈样本聚合成火焰图式调用树。
package tree

import "sync"

// Node 是调用树节点。Children 以帧名为键，保证插入每层 O(1)。
type Node struct {
	Frame     string
	Self      int64
	Total     int64
	Truncated bool // 该节点是深度截断点
	Children  map[string]*Node
}

// Tree 聚合样本并维护计数器。并发安全：插入写锁，查询先 Snapshot。
type Tree struct {
	mu               sync.RWMutex
	Root             *Node
	Samples          int64 // 已插入样本数，恒等于所有节点 Self 之和
	TruncatedSamples int64 // 被深度截断的样本数
	lookups          int64 // 插入时的查找/比较计数（复杂度证据）
	rebuilds         int64 // 归因重建树的次数，恒为 0
}

// New 返回空树，根帧名为空串。
func New() *Tree {
	return &Tree{Root: &Node{Children: map[string]*Node{}}}
}

// Insert 插入一条规范化后的栈，代价与栈深成正比。
func (t *Tree) Insert(frames []string, truncated bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	cur := t.Root
	cur.Total++
	for _, f := range frames {
		t.lookups++
		child, ok := cur.Children[f]
		if !ok {
			child = &Node{Frame: f, Children: map[string]*Node{}}
			cur.Children[f] = child
		}
		child.Total++
		cur = child
	}
	cur.Self++
	if truncated {
		cur.Truncated = true
		t.TruncatedSamples++
	}
	t.Samples++
}

// Attach 挂载一个带既有计数的子节点，仅供 dump 读回时重建树。
func (t *Tree) Attach(parent *Node, frame string, self, total int64, truncated bool) *Node {
	child := &Node{Frame: frame, Self: self, Total: total, Truncated: truncated, Children: map[string]*Node{}}
	parent.Children[frame] = child
	return child
}

// Snapshot 在读锁下深拷贝整棵树，调用方可离线遍历，不会看到半更新状态。
func (t *Tree) Snapshot() *Node {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return clone(t.Root)
}

func clone(n *Node) *Node {
	c := &Node{Frame: n.Frame, Self: n.Self, Total: n.Total, Truncated: n.Truncated, Children: map[string]*Node{}}
	for name, child := range n.Children {
		c.Children[name] = clone(child)
	}
	return c
}

// Lookups 返回插入累计的查找次数。
func (t *Tree) Lookups() int64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.lookups
}

// Rebuilds 返回归因导致的重建次数，设计恒为 0。
func (t *Tree) Rebuilds() int64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.rebuilds
}

// SumSelf 返回子树所有节点 Self 之和。
func SumSelf(n *Node) int64 {
	sum := n.Self
	for _, c := range n.Children {
		sum += SumSelf(c)
	}
	return sum
}

// SumTotal 返回子树所有节点 Total 之和（含祖先重复计数）。
func SumTotal(n *Node) int64 {
	sum := n.Total
	for _, c := range n.Children {
		sum += SumTotal(c)
	}
	return sum
}
