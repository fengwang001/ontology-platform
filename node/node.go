// Package node 定义区间树节点：键为 ival.Interval，维护 AVL 高度与
// 子树最大右端点 MaxR。本包只依赖 ival。
package node

import "ontology/ival"

// Node 是 AVL 区间树的一个节点。未导出字段由 tree 包直接维护。
type Node struct {
	Iv     ival.Interval
	Left   *Node
	Right  *Node
	Height int
	// MaxR 是以本节点为根的子树中最大的右端点。
	MaxR int64
}

// New 创建叶子节点；高度为 1，MaxR 即自身右端点。
func New(iv ival.Interval) *Node {
	return &Node{Iv: iv, Height: 1, MaxR: iv.R}
}

func h(n *Node) int {
	if n == nil {
		return 0
	}
	return n.Height
}

// refresh 依据左右子树重新计算 Height 与 MaxR。
// 所有结构调整（插入回溯、删除回溯、旋转）之后必须调用。
func (n *Node) refresh() {
	n.Height = 1 + max(h(n.Left), h(n.Right))
	n.MaxR = n.Iv.R
	if n.Left != nil && n.Left.MaxR > n.MaxR {
		n.MaxR = n.Left.MaxR
	}
	if n.Right != nil && n.Right.MaxR > n.MaxR {
		n.MaxR = n.Right.MaxR
	}
}

// Balance 返回 AVL 平衡因子：左高 - 右高。
func (n *Node) Balance() int {
	if n == nil {
		return 0
	}
	return h(n.Left) - h(n.Right)
}

// RotateRight 返回以 n 为根右旋后的新根，并修正高度与 MaxR。
func RotateRight(n *Node) *Node {
	x := n.Left
	t2 := x.Right
	x.Right = n
	n.Left = t2
	n.refresh()
	x.refresh()
	return x
}

// RotateLeft 返回以 n 为根左旋后的新根，并修正高度与 MaxR。
func RotateLeft(n *Node) *Node {
	y := n.Right
	t2 := y.Left
	y.Left = n
	n.Right = t2
	n.refresh()
	y.refresh()
	return y
}

// Rebalance 在 n 的子树数据已刷新后，按 AVL 规则重平衡并返回新根。
func Rebalance(n *Node) *Node {
	n.refresh()
	bf := h(n.Left) - h(n.Right)
	switch {
	case bf > 1 && h(n.Left.Left) >= h(n.Left.Right):
		return RotateRight(n)
	case bf > 1:
		n.Left = RotateLeft(n.Left)
		return RotateRight(n)
	case bf < -1 && h(n.Right.Right) >= h(n.Right.Left):
		return RotateLeft(n)
	case bf < -1:
		n.Right = RotateRight(n.Right)
		return RotateLeft(n)
	}
	return n
}

// Count 返回以 n 为根的子树节点数。
func Count(n *Node) int {
	if n == nil {
		return 0
	}
	return 1 + Count(n.Left) + Count(n.Right)
}
