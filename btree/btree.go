// Package btree implements a 2-3 tree (B-tree of order 3).
package btree

import (
	"errors"
	"slices"

	"ontology/bnode"
)

var (
	ErrDuplicate = errors.New("btree: key already exists")
	ErrNotFound  = errors.New("btree: key not found")
	ErrFull      = errors.New("btree: max keys exceeded")
)

// Tree is a 2-3 tree of distinct int keys.
type Tree struct {
	Root       *bnode.Node
	N, MaxKeys int // live key count / capacity
	visited    int // nodes touched by the last Search/Insert/Delete
}

func New(maxKeys int) *Tree { return &Tree{Root: &bnode.Node{}, MaxKeys: maxKeys} }
func (t *Tree) find(n *bnode.Node, k int) bool {
	for {
		t.visited++
		i, ok := n.Pos(k)
		if ok || n.Leaf() {
			return ok
		}
		n = n.Children[i]
	}
}
func (t *Tree) Search(k int) bool { t.visited = 0; return t.find(t.Root, k) }

// Insert adds k; cost is the number of splits (including root cascade).
func (t *Tree) Insert(k int) (int, error) {
	t.visited = 0
	if t.find(t.Root, k) {
		return 0, ErrDuplicate
	}
	if t.N >= t.MaxKeys {
		return 0, ErrFull
	}
	prom, right, cost, split := t.insert(t.Root, k)
	if split {
		t.Root = &bnode.Node{Keys: []int{prom}, Children: []*bnode.Node{t.Root, right}}
	}
	t.N++
	return cost, nil
}

func (t *Tree) insert(n *bnode.Node, k int) (prom int, right *bnode.Node, cost int, split bool) {
	t.visited++
	i, _ := n.Pos(k)
	if !n.Leaf() {
		if prom, right, cost, split = t.insert(n.Children[i], k); !split {
			return 0, nil, cost, false
		}
		k = prom
		n.Children = slices.Insert(n.Children, i+1, right)
	}
	n.Keys = slices.Insert(n.Keys, i, k)
	if len(n.Keys) < 3 {
		return 0, nil, cost, false
	}
	right = &bnode.Node{Keys: []int{n.Keys[2]}}
	if !n.Leaf() {
		right.Children = slices.Clone(n.Children[2:])
		n.Children = n.Children[:2]
	}
	prom, n.Keys = n.Keys[1], n.Keys[:1]
	return prom, right, cost + 1, true
}

// Delete removes k; cost is the number of borrows plus merges.
func (t *Tree) Delete(k int) (int, error) {
	t.visited = 0
	if !t.find(t.Root, k) {
		return 0, ErrNotFound
	}
	cost := t.del(t.Root, k)
	t.N--
	if !t.Root.Leaf() && len(t.Root.Keys) == 0 {
		t.Root = t.Root.Children[0] // root emptied by a merge: drop one level
	}
	return cost, nil
}

// del removes k below n; the caller fixes an underflowed child afterwards.
func (t *Tree) del(n *bnode.Node, k int) (cost int) {
	t.visited++
	i, ok := n.Pos(k)
	if n.Leaf() {
		n.Keys = slices.Delete(n.Keys, i, i+1)
		return 0
	}
	if ok { // replace k with its predecessor, delete that from the left subtree
		k = n.Children[i].Max()
		n.Keys[i] = k
	}
	cost = t.del(n.Children[i], k)
	if len(n.Children[i].Keys) == 0 {
		cost += t.fix(n, i)
	}
	return cost
}

// fix rebalances the empty child i of n by one borrow or one merge.
func (t *Tree) fix(n *bnode.Node, i int) int {
	kids := n.Children
	borrow := func(c, s *bnode.Node, pi int, fromLeft bool) {
		if fromLeft { // parent key pi down into c, s's max key up
			c.Keys = slices.Insert(c.Keys, 0, n.Keys[pi])
			n.Keys[pi] = s.Keys[1]
			s.Keys = s.Keys[:1]
			if !s.Leaf() {
				c.Children = slices.Insert(c.Children, 0, s.Children[2])
				s.Children = s.Children[:2]
			}
			return
		}
		c.Keys = append(c.Keys, n.Keys[pi])
		n.Keys[pi] = s.Keys[0]
		s.Keys = s.Keys[1:]
		if !s.Leaf() {
			c.Children = append(c.Children, s.Children[0])
			s.Children = s.Children[1:]
		}
	}
	if i > 0 && len(kids[i-1].Keys) == 2 {
		borrow(kids[i], kids[i-1], i-1, true)
		return 1
	}
	if i+1 < len(kids) && len(kids[i+1].Keys) == 2 {
		borrow(kids[i], kids[i+1], i, false)
		return 1
	}
	ci, si, pi := i, i+1, i // merge right sibling into child i
	if i > 0 {              // or merge child i into its left sibling
		ci, si, pi = i-1, i, i-1
	}
	c, s := kids[ci], kids[si]
	c.Keys = append(append(c.Keys, n.Keys[pi]), s.Keys...)
	c.Children = append(c.Children, s.Children...)
	n.Keys = slices.Delete(n.Keys, pi, pi+1)
	n.Children = slices.Delete(n.Children, si, si+1)
	return 1
}
