package tree

import (
	"ontology/ival"
	"ontology/node"
)

// Stab 返回包含点 x 的全部区间，按 (L,R) 稳定有序。
// onVisit 非 nil 时，每访问一个树节点回调一次，用于访问计数。
func (t *Tree) Stab(x int64, onVisit func()) []ival.Interval {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var out []ival.Interval
	var walk func(*node.Node)
	walk = func(n *node.Node) {
		if n == nil {
			return
		}
		if onVisit != nil {
			onVisit()
		}
		// 左子树最大右端点 <= x：左子树不可能含 x，剪枝。
		if n.Left == nil || n.Left.MaxR > x {
			walk(n.Left)
		}
		if n.Iv.Contains(x) {
			out = append(out, n.Iv)
		}
		// L>x 的节点及其右子树键都更大，不可能含 x。
		if n.Iv.L <= x {
			walk(n.Right)
		}
	}
	walk(t.root)
	return out
}

// Overlap 返回与 q 交点集非空的全部区间，按 (L,R) 稳定有序。
// 零长度 q 与任何区间都不相交，结果必为空。
func (t *Tree) Overlap(q ival.Interval, onVisit func()) []ival.Interval {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var out []ival.Interval
	var walk func(*node.Node)
	walk = func(n *node.Node) {
		if n == nil {
			return
		}
		if onVisit != nil {
			onVisit()
		}
		goLeft := n.Left != nil && n.Left.MaxR > q.L
		if goLeft {
			walk(n.Left)
		}
		if n.Iv.Overlaps(q) {
			out = append(out, n.Iv)
		}
		// 本节点 L>=q.R 时，右子树所有键的 L 更大，均不相交。
		if n.Iv.L < q.R {
			walk(n.Right)
		}
	}
	walk(t.root)
	return out
}

// Snapshot 按中序（(L,R) 稳定序）导出全部区间。
func (t *Tree) Snapshot() []ival.Interval {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]ival.Interval, 0, t.count)
	var walk func(*node.Node)
	walk = func(n *node.Node) {
		if n == nil {
			return
		}
		walk(n.Left)
		out = append(out, n.Iv)
		walk(n.Right)
	}
	walk(t.root)
	return out
}
