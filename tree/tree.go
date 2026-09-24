// Package tree 把采样到的调用栈聚合成火焰图式调用树。
// 每个节点记录 self（只落在本节点的样本数）与 total（经过本节点的样本数）。
package tree

import (
	"sync"

	"ontology/stack"
)

// Node 是调用树在某一时刻的只读快照节点。
type Node struct {
	Name      string
	Depth     int
	Truncated bool
	Self      uint64
	Total     uint64
	Children  []*Node
}

type node struct {
	name      string
	truncated bool
	self      uint64
	total     uint64
	children  map[string]*node
	order     []*node
}

// Tree 是并发安全的聚合调用树。零值不可用，须用 New 构造。
type Tree struct {
	mu        sync.RWMutex
	root      *node
	samples   uint64
	truncated uint64
	invalid   uint64
	insertCmp uint64 // 插入路径时 map 查找/比较次数
}

// New 返回空调用树。
func New() *Tree {
	return &Tree{root: &node{children: map[string]*node{}}}
}

// Insert 沿栈帧路径插入一条样本。空栈被拒绝并计入无效样本。
// 整次路径更新在同一写临界区内完成，读者不会看到半更新。
func (t *Tree) Insert(s stack.Stack) error {
	if len(s.Frames) == 0 {
		t.mu.Lock()
		t.invalid++
		t.mu.Unlock()
		return stack.ErrEmpty
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.samples++
	cur := t.root
	for i, f := range s.Frames {
		child, ok := cur.children[f.Name] // 一次与节点总数无关的直接定位
		t.insertCmp++
		if !ok {
			child = &node{name: f.Name, children: map[string]*node{}}
			cur.children[f.Name] = child
			cur.order = append(cur.order, child)
		}
		child.total++
		if i == len(s.Frames)-1 && s.Truncated {
			child.truncated = true
			t.truncated++
		}
		cur = child
	}
	cur.self++
	return nil
}

// InsertNames 是 Normalize + Insert 的便捷入口。
func (t *Tree) InsertNames(names []string, maxDepth int) error {
	s, err := stack.Normalize(names, maxDepth)
	if err != nil {
		t.mu.Lock()
		t.invalid++
		t.mu.Unlock()
		return err
	}
	return t.Insert(s)
}

// Samples 返回成功入树的样本总数。
func (t *Tree) Samples() uint64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.samples
}

// TruncatedSamples 返回在截断点终止的样本数。
func (t *Tree) TruncatedSamples() uint64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.truncated
}

// InvalidSamples 返回被拒绝的空栈样本数。
func (t *Tree) InvalidSamples() uint64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.invalid
}

// InsertComparisons 返回累计插入比较/查找次数（非导出计数器的只读视图）。
func (t *Tree) InsertComparisons() uint64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.insertCmp
}

// Snapshot 在读锁内一次性深拷贝出整棵树（虚拟根 Depth 为 -1）。
func (t *Tree) Snapshot() *Node {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return copyNode(t.root, -1)
}

func copyNode(n *node, depth int) *Node {
	cp := &Node{
		Name:      n.name,
		Depth:     depth,
		Truncated: n.truncated,
		Self:      n.self,
		Total:     n.total,
	}
	for _, ch := range n.order {
		cp.Children = append(cp.Children, copyNode(ch, depth+1))
	}
	return cp
}

// SumSelf 递归求快照子树 self 之和。
func SumSelf(n *Node) uint64 {
	if n == nil {
		return 0
	}
	var sum uint64 = n.Self
	for _, c := range n.Children {
		sum += SumSelf(c)
	}
	return sum
}

// SumTotal 递归求快照子树 total 之和（含重复计入的祖先）。
func SumTotal(n *Node) uint64 {
	if n == nil {
		return 0
	}
	var sum uint64 = n.Total
	for _, c := range n.Children {
		sum += SumTotal(c)
	}
	return sum
}
