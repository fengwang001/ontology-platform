// Package tree 把采样调用栈聚合成火焰图式调用树。
package tree

import (
	"sync"

	"ontology/stack"
)

// Node 是调用树上的一个节点（按调用路径区分，同名函数可出现多次）。
type Node struct {
	Name      string
	Self      int64
	Total     int64
	Truncated bool
	children  map[string]*Node
}

// Tree 是以合成根为起点的调用树，自身统计字段暴露于根节点之外。
type Tree struct {
	mu sync.RWMutex

	Root *Node

	Samples          int64 // 已接受的样本数（self 之和）
	InvalidSamples   int64 // 空栈等被拒绝的样本数
	TruncatedSamples int64 // 插入时被深度截断的样本数

	insertCompares int64 // 插入路径上的比较/查找次数
	treeBuilds     int64 // 归因触发的重建次数（须恒为 0）
	maxDepth       int   // <=0 表示不截断
}

// New 创建调用树。maxDepth<=0 表示不限制深度。
func New(maxDepth int) *Tree {
	return &Tree{
		Root:     &Node{children: map[string]*Node{}},
		maxDepth: maxDepth,
	}
}

// Insert 沿调用路径插入一条栈：路径每帧 total+1，末端 self+1。
// 空栈被拒绝并计入 InvalidSamples。深度超过上限时末帧带截断标记，
// 样本仍被接受并计入 TruncatedSamples。代价与栈深成正比。
func (t *Tree) Insert(s stack.Stack) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if s.Empty() {
		t.InvalidSamples++
		return
	}
	frames := s.Frames
	truncated := s.Truncated
	if t.maxDepth > 0 && len(frames) > t.maxDepth {
		frames = frames[:t.maxDepth]
		truncated = true
	}
	t.Samples++
	if truncated {
		t.TruncatedSamples++
	}
	cur := t.Root
	for i := range frames {
		name := frames[i].Name
		child := cur.children[name] // 1 次查找比较
		t.insertCompares++
		if child == nil {
			child = &Node{Name: name, children: map[string]*Node{}}
			cur.children[name] = child // 插入写，再计 1 次比较
			t.insertCompares++
		}
		child.Total++
		if i == len(frames)-1 {
			child.Self++
			if truncated {
				child.Truncated = true
			}
		}
		cur = child
	}
}

// InsertNames 是 Insert 的便捷形式。
func (t *Tree) InsertNames(names ...string) { t.Insert(stack.FromNames(names...)) }

// Stats 返回插入比较次数、重建次数（归因用，恒为 0）与上限。
func (t *Tree) Stats() (insertCompares, treeBuilds int64, maxDepth int) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.insertCompares, t.treeBuilds, t.maxDepth
}

// RLock/RUnlock 供 attrib 在一致快照上遍历，杜绝半更新可见。
func (t *Tree) RLock()   { t.mu.RLock() }
func (t *Tree) RUnlock() { t.mu.RUnlock() }

// Child 按名取直接子节点。
func (n *Node) Child(name string) *Node { return n.children[name] }

// Children 返回子节点无序切片。
func (n *Node) Children() []*Node {
	out := make([]*Node, 0, len(n.children))
	for _, c := range n.children {
		out = append(out, c)
	}
	return out
}

// SelfSum 对以 n 为根的整棵子树求 self 之和。
func (n *Node) SelfSum() int64 {
	sum := n.Self
	for _, c := range n.children {
		sum += c.SelfSum()
	}
	return sum
}

// TotalSum 对以 n 为根的整棵子树求 total 之和（祖先重复计入）。
func (n *Node) TotalSum() int64 {
	sum := n.Total
	for _, c := range n.children {
		sum += c.TotalSum()
	}
	return sum
}

// NodeCount 统计以 n 为根的节点数（含 n 自身）。
func (n *Node) NodeCount() int {
	count := 1
	for _, c := range n.children {
		count += c.NodeCount()
	}
	return count
}
