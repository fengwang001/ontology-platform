// Package tree 把规范化后的调用栈聚合成调用树（self/total）。
package tree

import (
	"sync"

	"ontology/stack"
)

// Node 是调用树节点。Root 为虚拟根，其直接子节点是栈顶帧。
type Node struct {
	Frame     stack.Frame
	Self      int
	Total     int
	Truncated bool
	Children  []*Node
}

type childNode struct {
	node *Node
}

// Tree 是可并发安全查询的调用树聚合器。
type Tree struct {
	mu          sync.RWMutex
	root        *Node
	childIndex  map[*Node]map[string]*Node
	samples     int
	truncated   int
	invalid     int
	lookupCount int // 插入时的比较/查找累计次数
}

// New 创建空树。
func New() *Tree {
	r := &Node{}
	return &Tree{root: r, childIndex: map[*Node]map[string]*Node{r: {}}}
}

func frameKey(f stack.Frame) string {
	return f.Func + "\x00" + f.File + "\x00" + string(rune(f.Line))
}

// Insert 在写锁下沿栈路径插入一条样本，绝不半更新。
func (t *Tree) Insert(r stack.Result) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if r.Invalid || len(r.Frames) == 0 {
		t.invalid++
		return
	}
	t.samples++
	if r.Truncated {
		t.truncated++
	}
	cur := t.root
	for i, f := range r.Frames {
		idx := t.childIndex[cur]
		key := frameKey(f)
		t.lookupCount++
		child, ok := idx[key]
		if !ok {
			child = &Node{Frame: f, Truncated: i == len(r.Frames)-1 && f.Truncated}
			cur.Children = append(cur.Children, child)
			t.childIndex[child] = map[string]*Node{}
			idx[key] = child
		}
		child.Total++
		if i == len(r.Frames)-1 {
			child.Self++
		}
		cur = child
	}
}

// Stats 返回读侧计数器。
func (t *Tree) Stats() (samples, truncated, invalid, lookups int) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.samples, t.truncated, t.invalid, t.lookupCount
}

// Snapshot 返回整树深拷贝，供锁外归因使用。
func (t *Tree) Snapshot() *Node {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return clone(t.root)
}

func clone(n *Node) *Node {
	c := &Node{Frame: n.Frame, Self: n.Self, Total: n.Total, Truncated: n.Truncated}
	for _, ch := range n.Children {
		c.Children = append(c.Children, clone(ch))
	}
	return c
}
