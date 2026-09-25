// Package bnode defines the node structure of an order-3 B-tree (2-3 tree)
// plus key location, in-order traversal and structural validation.
// It must not depend on any other project package.
package bnode

import "errors"

// Node is a 2-3 tree node. A leaf has Keys and no Kids. An internal node
// always has len(Kids) == len(Keys)+1. Non-root nodes hold 1 or 2 keys;
// the root may hold 0 (empty tree) to 2 keys.
type Node struct {
	Keys []int
	Kids []*Node
}

// IsLeaf reports whether n is a leaf node.
func (n *Node) IsLeaf() bool { return len(n.Kids) == 0 }

// FindIndex returns the first index i with Keys[i] >= k. The return value is
// also the index of the child that may contain k (0..len(Keys)).
func (n *Node) FindIndex(k int) int {
	lo, hi := 0, len(n.Keys)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if n.Keys[mid] < k {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

// Inorder appends every key in the subtree rooted at n in strictly ascending
// order to dst and returns the resulting slice.
func (n *Node) Inorder(dst []int) []int {
	if n == nil {
		return dst
	}
	if n.IsLeaf() {
		return append(dst, n.Keys...)
	}
	for i, kid := range n.Kids {
		dst = kid.Inorder(dst)
		if i < len(n.Keys) {
			dst = append(dst, n.Keys[i])
		}
	}
	return dst
}

// Structural validation errors surfaced by Tree.SelfCheck.
var (
	errBadCount = errors.New("bnode: key count out of [1,2] (root may be 0)")
	errBadOrder = errors.New("bnode: keys not strictly ascending")
	errBadKids  = errors.New("bnode: internal node kids != keys+1")
	errBadDepth = errors.New("bnode: leaves not all at the same depth")
	errBadBound = errors.New("bnode: subtree key violates separator bounds")
)

// Validate verifies the whole subtree as if n were the root: key-count bounds,
// child count, equal leaf depth, key ordering and subtree key bounds.
func (n *Node) Validate() error {
	if n == nil {
		return nil
	}
	depth := 0
	for cur := n; !cur.IsLeaf(); cur = cur.Kids[0] {
		if len(cur.Kids) == 0 {
			return errBadKids
		}
		depth++
	}
	return n.validate(true, depth, 0, 0, 0, false, false)
}

// validate enforces key counts, ordering, depth and bounds lo < every key < hi
// for the bounds that are present.
func (n *Node) validate(isRoot bool, wantDepth, depth, lo, hi int, hasLo, hasHi bool) error {
	if nk := len(n.Keys); nk > 2 || (!isRoot && nk < 1) {
		return errBadCount
	}
	for i := 1; i < len(n.Keys); i++ {
		if n.Keys[i-1] >= n.Keys[i] {
			return errBadOrder
		}
	}
	for _, k := range n.Keys {
		if hasLo && k <= lo {
			return errBadBound
		}
		if hasHi && k >= hi {
			return errBadBound
		}
	}
	if n.IsLeaf() {
		if depth != wantDepth {
			return errBadDepth
		}
		return nil
	}
	if len(n.Kids) != len(n.Keys)+1 {
		return errBadKids
	}
	for i, kid := range n.Kids {
		if kid == nil {
			return errBadKids
		}
		cLo, cHi, cHasLo, cHasHi := lo, hi, hasLo, hasHi
		if i > 0 {
			cLo, cHasLo = n.Keys[i-1], true
		}
		if i < len(n.Keys) {
			cHi, cHasHi = n.Keys[i], true
		}
		if err := kid.validate(false, wantDepth, depth+1, cLo, cHi, cHasLo, cHasHi); err != nil {
			return err
		}
	}
	return nil
}
