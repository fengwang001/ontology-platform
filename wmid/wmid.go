// Package wmid 提供按 value 组织的 AVL 平衡二叉树，
// 每个节点存自身权重与子树权重和，支持 O(log n) 插入。
package wmid

import "errors"

var (
	// ErrNonPositiveWeight 权重非正（weight <= 0）。
	ErrNonPositiveWeight = errors.New("wmid: weight must be positive")
	// ErrDuplicateValue 插入的 value 已存在。
	ErrDuplicateValue = errors.New("wmid: value already exists")
)

// Node 是树节点。Sum 为以该节点为根的子树权重和。
type Node struct {
	Value       int64
	Weight      int64
	Sum         int64
	height      int
	left, right *Node
}

// Left 返回左子节点，Right 返回右子节点。
func (n *Node) Left() *Node  { return n.left }
func (n *Node) Right() *Node { return n.right }

// Tree 是按 value 升序组织的平衡树。
type Tree struct {
	root *Node
}

// New 返回一棵空树。
func New() *Tree { return &Tree{} }

// Root 返回根节点（空树为 nil），供查询方沿树遍历。
func (t *Tree) Root() *Node { return t.root }

// Depth 返回树高（空树为 0）。
func (t *Tree) Depth() int { return height(t.root) }

// Insert 插入 (value, weight)。weight <= 0 或 value 已存在时
// 整体失败，树不发生任何变化。
func (t *Tree) Insert(value, weight int64) error {
	if weight <= 0 {
		return ErrNonPositiveWeight
	}
	if contains(t.root, value) {
		return ErrDuplicateValue
	}
	t.root = insert(t.root, value, weight)
	return nil
}

// InOrder 按 value 升序对每个元素调用 fn。
func (t *Tree) InOrder(fn func(value, weight int64)) {
	var walk func(n *Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		walk(n.left)
		fn(n.Value, n.Weight)
		walk(n.right)
	}
	walk(t.root)
}

func height(n *Node) int {
	if n == nil {
		return 0
	}
	return n.height
}

func sum(n *Node) int64 {
	if n == nil {
		return 0
	}
	return n.Sum
}

func (n *Node) recalc() {
	n.Sum = n.Weight + sum(n.left) + sum(n.right)
	if lh, rh := height(n.left), height(n.right); lh > rh {
		n.height = lh + 1
	} else {
		n.height = rh + 1
	}
}

func contains(n *Node, value int64) bool {
	for n != nil {
		switch {
		case value < n.Value:
			n = n.left
		case value > n.Value:
			n = n.right
		default:
			return true
		}
	}
	return false
}

func insert(n *Node, value, weight int64) *Node {
	if n == nil {
		return &Node{Value: value, Weight: weight, Sum: weight, height: 1}
	}
	if value < n.Value {
		n.left = insert(n.left, value, weight)
	} else {
		n.right = insert(n.right, value, weight)
	}
	return balance(n)
}

func balance(n *Node) *Node {
	n.recalc()
	switch bf := height(n.left) - height(n.right); {
	case bf > 1:
		if height(n.left.left) < height(n.left.right) {
			n.left = rotateLeft(n.left)
		}
		return rotateRight(n)
	case bf < -1:
		if height(n.right.right) < height(n.right.left) {
			n.right = rotateRight(n.right)
		}
		return rotateLeft(n)
	}
	return n
}

func rotateLeft(n *Node) *Node {
	r := n.right
	n.right = r.left
	r.left = n
	n.recalc()
	r.recalc()
	return r
}

func rotateRight(n *Node) *Node {
	l := n.left
	n.left = l.right
	l.right = n
	n.recalc()
	l.recalc()
	return l
}
