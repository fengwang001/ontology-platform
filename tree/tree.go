// Package tree 把采样栈按调用路径聚合成火焰图式调用树，
// 维护每个节点的 self/total 计数，并保证 Σself == 总采样数。
package tree

import "ontology/stack"

// Node 是调用树节点，对应一条根到本节点的帧路径。
type Node struct {
	Frame     string
	Self      uint64 // 路径恰好终止于本节点的采样数
	Total     uint64 // 路径经过本节点的采样数（含整棵子树）
	Truncated bool   // 本节点是某次深度截断的截断点
	Children  []*Node
}

// Tree 是聚合后的调用树。Root 是虚拟根（Frame 为空），其 Total 等于总采样数。
type Tree struct {
	Root             *Node
	Samples          uint64 // 已插入的采样总数（含被截断的）
	TruncatedSamples uint64 // 因超过深度上限被截断的采样数
	lookups          uint64 // 插入时子节点比较/查找次数（非导出计数器）
}

// New 返回一棵空树。
func New() *Tree { return &Tree{Root: &Node{}} }

// Insert 把一条规范化后的栈插入树中：路径上每个节点 Total+1，
// 仅栈顶节点 Self+1；截断栈在截断点节点上打 Truncated 标记。
// 代价与栈深成正比，与已有节点总数无关。
func (t *Tree) Insert(s stack.Stack) {
	t.Samples++
	if s.Truncated {
		t.TruncatedSamples++
	}
	n := t.Root
	n.Total++
	last := len(s.Frames) - 1
	for i, f := range s.Frames {
		child := n.findChild(f, &t.lookups)
		if child == nil {
			child = &Node{Frame: f}
			n.Children = append(n.Children, child)
		}
		child.Total++
		if i == last {
			child.Self++
			if s.Truncated {
				child.Truncated = true
			}
		}
		n = child
	}
}

func (n *Node) findChild(frame string, lookups *uint64) *Node {
	for _, c := range n.Children {
		*lookups++
		if c.Frame == frame {
			return c
		}
	}
	return nil
}

// Lookups 返回插入过程中累计的子节点比较/查找次数。
func (t *Tree) Lookups() uint64 { return t.lookups }

// SumSelf 返回所有节点 self 之和，恒等于 Samples。
func (t *Tree) SumSelf() uint64 { return sumSelf(t.Root) }

func sumSelf(n *Node) uint64 {
	s := n.Self
	for _, c := range n.Children {
		s += sumSelf(c)
	}
	return s
}

// SumTotal 返回所有节点 total 之和（含虚拟根），一般大于 Samples。
func (t *Tree) SumTotal() uint64 { return sumTotal(t.Root) }

func sumTotal(n *Node) uint64 {
	s := n.Total
	for _, c := range n.Children {
		s += sumTotal(c)
	}
	return s
}

// Snapshot 返回整树的深拷贝，供并发读方获得一致性视图。
func (t *Tree) Snapshot() *Tree {
	cp := *t
	cp.Root = t.Root.clone()
	return &cp
}

func (n *Node) clone() *Node {
	cp := *n
	cp.Children = make([]*Node, len(n.Children))
	for i, c := range n.Children {
		cp.Children[i] = c.clone()
	}
	return &cp
}

// Walk 以前序（父先于子）遍历所有节点，depth 从 0（虚拟根）开始。
func (t *Tree) Walk(fn func(n *Node, depth int)) {
	var rec func(n *Node, depth int)
	rec = func(n *Node, depth int) {
		fn(n, depth)
		for _, c := range n.Children {
			rec(c, depth+1)
		}
	}
	rec(t.Root, 0)
}
