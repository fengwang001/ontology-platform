// Package node defines the AVL tree node and maintains the
// subtree maximum right endpoint (MaxHi) invariant locally.
package node

import "ontology/ival"

// Node is one AVL tree node keyed by its interval.
type Node struct {
	Iv     ival.Interval
	Left   *Node
	Right  *Node
	Height int   // height of the subtree rooted here; leaf = 1
	MaxHi  int64 // maximum Hi over the subtree rooted here
}

// New returns a leaf node holding iv.
func New(iv ival.Interval) *Node {
	return &Node{Iv: iv, Height: 1, MaxHi: iv.Hi}
}

// HeightOf returns the height of n, treating nil as 0.
func HeightOf(n *Node) int {
	if n == nil {
		return 0
	}
	return n.Height
}

// MaxHiOf returns the subtree maximum Hi of n, treating nil as the
// minimum int64 so leaves fall back to their own Hi.
func MaxHiOf(n *Node) int64 {
	if n == nil {
		return minInt64
	}
	return n.MaxHi
}

const minInt64 = -int64(1) << 63

// Update recomputes Height and MaxHi from the children. It must be
// called after any change to a node's children or interval.
func (n *Node) Update() {
	n.Height = 1 + max(HeightOf(n.Left), HeightOf(n.Right))
	n.MaxHi = max(n.Iv.Hi, max(MaxHiOf(n.Left), MaxHiOf(n.Right)))
}

// Balance returns the balance factor (left height - right height).
func (n *Node) Balance() int {
	return HeightOf(n.Left) - HeightOf(n.Right)
}
