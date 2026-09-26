// Package wmid holds the value-ordered balanced structure used for weighted
// median lookups: a randomized treap whose nodes carry their own weight and
// the total weight of their subtree. It depends on no other package.
package wmid

import "errors"

// Sentinel errors for rejected inserts. Both leave the tree untouched.
var (
	ErrNonPositiveWeight = errors.New("wmid: weight must be > 0")
	ErrDuplicateValue    = errors.New("wmid: value already exists")
)

// Node is one inserted element. Exported fields are the read-only view used
// to walk the tree; priority is internal.
type Node struct {
	Value  int64
	Weight int64
	Sum    int64 // total weight of this node's subtree
	Left   *Node
	Right  *Node

	priority uint64
}

// Tree is a treap keyed by Value, min-heap ordered by priority.
type Tree struct {
	root *Node
}

// Root returns the root node (nil when empty) for tree walks.
func (t *Tree) Root() *Node { return t.root }

// Insert adds (value, weight). A rejected insert (non-positive weight or
// duplicate value) mutates nothing: the original subtree is returned as-is.
func (t *Tree) Insert(value, weight int64) error {
	if weight <= 0 {
		return ErrNonPositiveWeight
	}
	root, err := insert(t.root, value, weight)
	if err != nil {
		return err
	}
	t.root = root
	return nil
}

func insert(n *Node, value, weight int64) (*Node, error) {
	if n == nil {
		return &Node{Value: value, Weight: weight, Sum: weight, priority: prio(value)}, nil
	}
	if value == n.Value {
		return n, ErrDuplicateValue
	}
	if value < n.Value {
		l, err := insert(n.Left, value, weight)
		if err != nil {
			return n, err // no assignment happened: state unchanged
		}
		n.Left = l
		if n.Left.priority < n.priority {
			n = rotateRight(n)
		}
	} else {
		r, err := insert(n.Right, value, weight)
		if err != nil {
			return n, err
		}
		n.Right = r
		if n.Right.priority < n.priority {
			n = rotateLeft(n)
		}
	}
	n.pull()
	return n, nil
}

func rotateRight(n *Node) *Node {
	l := n.Left
	n.Left = l.Right
	l.Right = n
	n.pull()
	l.pull()
	return l
}

func rotateLeft(n *Node) *Node {
	r := n.Right
	n.Right = r.Left
	r.Left = n
	n.pull()
	r.pull()
	return r
}

func (n *Node) pull() {
	n.Sum = n.Weight + sumOf(n.Left) + sumOf(n.Right)
}

func sumOf(n *Node) int64 {
	if n == nil {
		return 0
	}
	return n.Sum
}

// prio derives a deterministic, well-spread priority from value
// (splitmix64), so tree shape depends only on the value set, never on
// insertion order.
func prio(value int64) uint64 {
	x := uint64(value) + 0x9e3779b97f4a7c15
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	x ^= x >> 31
	return x
}
