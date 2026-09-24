// Package ost implements a balanced (AVL) order-statistic tree over a multiset of int64.
package ost

import "errors"

var ErrNotFound = errors.New("ost: value not found")
var ErrOutOfRange = errors.New("ost: k out of range")

type node struct {
	val  int64
	cnt  int // multiplicity of val at this node
	size int // elements in this subtree (sum of cnt)
	h    int
	l, r *node
}

// Tree is an AVL order-statistic tree; the zero value is ready to use.
type Tree struct{ root *node }

func size(n *node) int {
	if n == nil {
		return 0
	}
	return n.size
}
func height(n *node) int {
	if n == nil {
		return 0
	}
	return n.h
}
func balance(n *node) int { return height(n.l) - height(n.r) }
func (n *node) recalc() {
	n.size = n.cnt + size(n.l) + size(n.r)
	n.h = 1 + max(height(n.l), height(n.r))
}
func rotateLeft(n *node) *node {
	r := n.r
	n.r, r.l = r.l, n
	n.recalc()
	r.recalc()
	return r
}
func rotateRight(n *node) *node {
	l := n.l
	n.l, l.r = l.r, n
	n.recalc()
	l.recalc()
	return l
}
func rebalance(n *node) *node {
	switch b := balance(n); {
	case b > 1:
		if balance(n.l) < 0 {
			n.l = rotateLeft(n.l)
		}
		return rotateRight(n)
	case b < -1:
		if balance(n.r) > 0 {
			n.r = rotateRight(n.r)
		}
		return rotateLeft(n)
	}
	return n
}
func (t *Tree) Insert(v int64) { t.root = insert(t.root, v) }

func insert(n *node, v int64) *node {
	if n == nil {
		return &node{val: v, cnt: 1, size: 1, h: 1}
	}
	if v < n.val {
		n.l = insert(n.l, v)
	} else if v > n.val {
		n.r = insert(n.r, v)
	} else {
		n.cnt++
	}
	n.recalc()
	return rebalance(n)
}

// Delete removes one occurrence of v; absent v reports ErrNotFound and leaves the tree untouched.
func (t *Tree) Delete(v int64) error {
	root, ok := remove(t.root, v)
	if !ok {
		return ErrNotFound
	}
	t.root = root
	return nil
}

func remove(n *node, v int64) (*node, bool) {
	if n == nil {
		return nil, false
	}
	var ok bool
	switch {
	case v < n.val:
		n.l, ok = remove(n.l, v)
	case v > n.val:
		n.r, ok = remove(n.r, v)
	default:
		if n.cnt > 1 {
			n.cnt--
			n.recalc()
			return n, true
		}
		if n.l == nil {
			return n.r, true
		}
		if n.r == nil {
			return n.l, true
		}
		s := n.r
		for s.l != nil {
			s = s.l
		}
		n.val, n.cnt, s.cnt = s.val, s.cnt, 1 // adopt successor's count, then drop the successor node
		n.r, ok = remove(n.r, s.val)
	}
	if !ok {
		return n, false
	}
	n.recalc()
	return rebalance(n), true
}

func (t *Tree) Count() int { return size(t.root) }

// Kth returns the k-th smallest value (1-based); visits = nodes touched by the descent.
func (t *Tree) Kth(k int) (v int64, visits int, err error) {
	if k < 1 || k > size(t.root) {
		return 0, 0, ErrOutOfRange
	}
	n := t.root
	for {
		visits++
		ls := size(n.l)
		if k <= ls {
			n = n.l
			continue
		}
		if k <= ls+n.cnt {
			return n.val, visits, nil
		}
		k -= ls + n.cnt
		n = n.r
	}
}
