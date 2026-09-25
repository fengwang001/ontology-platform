// Package bnode defines the 2-3 tree node, key location and inorder traversal.
package bnode

import "sort"

// Node is a 2-3 tree node: 1..2 keys (root may hold 0); an internal node
// always has len(Keys)+1 children.
type Node struct {
	Keys     []int
	Children []*Node
}

// Leaf reports whether n is a leaf.
func (n *Node) Leaf() bool { return len(n.Children) == 0 }

// Pos returns the index where k is or would be inserted, and whether Keys[i] == k.
func (n *Node) Pos(k int) (int, bool) {
	i := sort.SearchInts(n.Keys, k)
	return i, i < len(n.Keys) && n.Keys[i] == k
}

// Inorder appends all keys in ascending order to dst and returns the result.
func (n *Node) Inorder(dst []int) []int {
	if n.Leaf() {
		return append(dst, n.Keys...)
	}
	for i, k := range n.Keys {
		dst = n.Children[i].Inorder(dst)
		dst = append(dst, k)
	}
	return n.Children[len(n.Keys)].Inorder(dst)
}

// Max returns the largest key in the subtree rooted at n.
func (n *Node) Max() int {
	for !n.Leaf() {
		n = n.Children[len(n.Children)-1]
	}
	return n.Keys[len(n.Keys)-1]
}
