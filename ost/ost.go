package ost

import "errors"

var ErrAbsent = errors.New("ost: value not present") // Delete missed
var ErrKthRange = errors.New("ost: k out of range")  // k outside [1,n]
type node struct {
	val         int64
	dup         int // value occurrences: multiset, not a set
	left, right *node
	h, sz       int // 1-based height; multiset subtree size
}

func hs(n *node) (h, s int) {
	if n != nil {
		h, s = n.h, n.sz
	}
	return
}
func (n *node) pull() {
	lh, ls := hs(n.left)
	rh, rs := hs(n.right)
	n.h, n.sz = 1+max(lh, rh), n.dup+ls+rs
}
func rotR(n *node) *node {
	x := n.left
	n.left, x.right = x.right, n
	n.pull()
	x.pull()
	return x
}
func rotL(n *node) *node {
	x := n.right
	n.right, x.left = x.left, n
	n.pull()
	x.pull()
	return x
}
func balance(n *node) *node {
	n.pull()
	lh, _ := hs(n.left)
	rh, _ := hs(n.right)
	if lh > rh+1 {
		llh, _ := hs(n.left.left)
		lrh, _ := hs(n.left.right)
		if llh < lrh {
			n.left = rotL(n.left)
		}
		return rotR(n)
	}
	if rh > lh+1 {
		rlh, _ := hs(n.right.left)
		rrh, _ := hs(n.right.right)
		if rrh < rlh {
			n.right = rotR(n.right)
		}
		return rotL(n)
	}
	return n
}
func insert(n *node, v int64) *node {
	if n == nil {
		return &node{val: v, dup: 1, h: 1, sz: 1}
	}
	switch {
	case v < n.val:
		n.left = insert(n.left, v)
	case v > n.val:
		n.right = insert(n.right, v)
	default:
		n.dup++
	}
	return balance(n)
}
func erase(n *node, v int64) (*node, bool) {
	if n == nil {
		return nil, false
	}
	switch {
	case v < n.val:
		l, ok := erase(n.left, v)
		if !ok {
			return n, false
		}
		n.left = l
	case v > n.val:
		r, ok := erase(n.right, v)
		if !ok {
			return n, false
		}
		n.right = r
	default:
		if n.dup > 1 {
			n.dup--
			return balance(n), true
		}
		switch {
		case n.left == nil:
			return n.right, true
		case n.right == nil:
			return n.left, true
		}
		s := n.right // in-order successor, carries its whole dup count
		for s.left != nil {
			s = s.left
		}
		n.val, n.dup = s.val, s.dup
		n.right = removeMin(n.right)
	}
	return balance(n), true
}
func removeMin(n *node) *node {
	if n.left == nil {
		return n.right
	}
	n.left = removeMin(n.left)
	return balance(n)
}

type Tree struct{ root *node }

func (t *Tree) Insert(v int64) { t.root = insert(t.root, v) }
func (t *Tree) Delete(v int64) error {
	r, ok := erase(t.root, v)
	if !ok {
		return ErrAbsent
	}
	t.root = r
	return nil
}
func (t *Tree) Count() int               { _, s := hs(t.root); return s }
func (t *Tree) Kth(k int) (int64, error) { return t.KthInto(k, func() {}) }

func (t *Tree) KthInto(k int, down func()) (int64, error) {
	for n := t.root; n != nil; {
		down()
		_, ls := hs(n.left)
		switch {
		case k <= ls:
			n = n.left
		case k <= ls+n.dup:
			return n.val, nil
		default:
			k -= ls + n.dup
			n = n.right
		}
	}
	return 0, ErrKthRange // k<1, k>Count, or empty all fall off the tree
}
