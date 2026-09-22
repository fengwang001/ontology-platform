// Package tree implements an AVL tree of intervals maintaining the
// subtree maximum right endpoint invariant. It is not goroutine-safe;
// concurrent read-only queries are safe once writes have stopped.
package tree

import (
	"errors"
	"fmt"
	"math"

	"ontology/ival"
	"ontology/node"
)

var (
	// ErrNotFound is returned when deleting an interval not in the tree.
	ErrNotFound = errors.New("tree: interval not found")
	// ErrTooMany is returned when the configured interval limit is hit.
	ErrTooMany = errors.New("tree: interval count limit exceeded")
)

// Tree is an ordered multiset of intervals keyed by ival.Compare.
type Tree struct {
	root *node.Node
	size int
	max  int // 0 means unlimited
}

// Option configures a Tree.
type Option func(*Tree)

// WithMaxIntervals caps the number of intervals; 0 means unlimited.
func WithMaxIntervals(n int) Option { return func(t *Tree) { t.max = n } }

// New returns an empty Tree.
func New(opts ...Option) *Tree {
	t := &Tree{}
	for _, o := range opts {
		o(t)
	}
	return t
}

// Size returns the number of intervals (multiset cardinality).
func (t *Tree) Size() int { return t.size }

// Root exposes the root for read-only traversal by the query package.
func (t *Tree) Root() *node.Node { return t.root }

// Insert adds one interval. Invalid intervals and limit overflows are
// rejected before any state changes.
func (t *Tree) Insert(iv ival.Interval) error {
	if !iv.Valid() {
		return ival.ErrInvalid
	}
	if t.max > 0 && t.size >= t.max {
		return ErrTooMany
	}
	t.root = insert(t.root, iv)
	t.size++
	return nil
}

func insert(n *node.Node, iv ival.Interval) *node.Node {
	if n == nil {
		return node.New(iv)
	}
	if ival.Compare(iv, n.Iv) <= 0 {
		n.Left = insert(n.Left, iv)
	} else {
		n.Right = insert(n.Right, iv)
	}
	return rebalance(n)
}

// Delete removes exactly one occurrence of iv.
func (t *Tree) Delete(iv ival.Interval) error {
	if !iv.Valid() {
		return ival.ErrInvalid
	}
	root, ok := remove(t.root, iv)
	if !ok {
		return ErrNotFound
	}
	t.root = root
	t.size--
	return nil
}

func remove(n *node.Node, iv ival.Interval) (*node.Node, bool) {
	if n == nil {
		return nil, false
	}
	var ok bool
	switch c := ival.Compare(iv, n.Iv); {
	case c < 0:
		n.Left, ok = remove(n.Left, iv)
	case c > 0:
		n.Right, ok = remove(n.Right, iv)
	default: // remove this node (first equal key on the search path)
		ok = true
		if n.Left == nil {
			return n.Right, true
		}
		if n.Right == nil {
			return n.Left, true
		}
		succ := n.Right
		for succ.Left != nil {
			succ = succ.Left
		}
		n.Iv = succ.Iv
		n.Right, _ = remove(n.Right, succ.Iv)
	}
	if !ok {
		return n, false
	}
	return rebalance(n), true
}

func rebalance(n *node.Node) *node.Node {
	n.Update()
	switch b := n.Balance(); {
	case b > 1:
		if n.Left.Balance() < 0 {
			n.Left = rotateLeft(n.Left)
		}
		return rotateRight(n)
	case b < -1:
		if n.Right.Balance() > 0 {
			n.Right = rotateRight(n.Right)
		}
		return rotateLeft(n)
	}
	return n
}

func rotateLeft(n *node.Node) *node.Node {
	r := n.Right
	n.Right = r.Left
	r.Left = n
	n.Update()
	r.Update()
	return r
}

func rotateRight(n *node.Node) *node.Node {
	l := n.Left
	n.Left = l.Right
	l.Right = n
	n.Update()
	l.Update()
	return l
}

// Check verifies the structural invariants: correct MaxHi on every
// node, AVL balance, height within the AVL bound, and node count
// equal to Size. It returns nil when all hold.
func (t *Tree) Check() error {
	count, height, err := check(t.root)
	if err != nil {
		return err
	}
	if count != t.size {
		return fmt.Errorf("tree: node count %d != size %d", count, t.size)
	}
	if bound := maxAVLHeight(count); height > bound {
		return fmt.Errorf("tree: height %d exceeds AVL bound %d", height, bound)
	}
	return nil
}

func check(n *node.Node) (count, height int, err error) {
	if n == nil {
		return 0, 0, nil
	}
	lc, lh, err := check(n.Left)
	if err != nil {
		return 0, 0, err
	}
	rc, rh, err := check(n.Right)
	if err != nil {
		return 0, 0, err
	}
	want := max(n.Iv.Hi, max(node.MaxHiOf(n.Left), node.MaxHiOf(n.Right)))
	if n.MaxHi != want {
		return 0, 0, fmt.Errorf("tree: MaxHi %d != actual %d at %v", n.MaxHi, want, n.Iv)
	}
	if b := n.Balance(); b < -1 || b > 1 {
		return 0, 0, fmt.Errorf("tree: balance factor %d at %v", b, n.Iv)
	}
	return lc + rc + 1, max(lh, rh) + 1, nil
}

// maxAVLHeight returns the largest height an AVL tree of n nodes may have.
func maxAVLHeight(n int) int {
	return int(1.4405*math.Log2(float64(n)+2)) + 1
}
