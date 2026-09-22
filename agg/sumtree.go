package agg

import "hash/fnv"

// sumNode is one treap node holding one member's value. prio is a fixed
// hash of the key, so the tree shape depends only on the current key set
// and not on change arrival order. total is the pairwise-float64 reduction
// of the subtree performed in in-order (sorted key) layout, which makes
// maintained state bit-identical to summing sorted surviving members.
type sumNode struct {
	key      string
	val      float64
	prio     uint32
	left     *sumNode
	right    *sumNode
	total    float64
	nonEmpty bool
}

func priority(key string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return h.Sum32()
}

func (n *sumNode) pull() {
	n.total, n.nonEmpty = combine(n.left, n.val, n.right)
}

// combine returns the in-order pairwise sum: left subtotal + node value,
// then + right subtotal, skipping empty sides. Using exactly the same
// folding order as sorted full recomputation gives IEEE-754 bit equality.
func combine(left *sumNode, val float64, right *sumNode) (float64, bool) {
	s := val
	if left != nil && left.nonEmpty {
		s = left.total + s
	}
	if right != nil && right.nonEmpty {
		s = s + right.total
	}
	return s, true
}

func rotateRight(n *sumNode) *sumNode {
	l := n.left
	n.left = l.right
	l.right = n
	n.pull()
	l.pull()
	return l
}

func rotateLeft(n *sumNode) *sumNode {
	r := n.right
	n.right = r.left
	r.left = n
	n.pull()
	r.pull()
	return r
}

func (n *sumNode) insert(key string, val float64) *sumNode {
	if n == nil {
		z := &sumNode{key: key, val: val, prio: priority(key)}
		z.pull()
		return z
	}
	switch {
	case key < n.key:
		n.left = n.left.insert(key, val)
		if n.left.prio > n.prio {
			n = rotateRight(n)
		}
	case key > n.key:
		n.right = n.right.insert(key, val)
		if n.right.prio > n.prio {
			n = rotateLeft(n)
		}
	default:
		n.val = val
	}
	n.pull()
	return n
}

func (n *sumNode) erase(key string) *sumNode {
	if n == nil {
		return nil
	}
	switch {
	case key < n.key:
		n.left = n.left.erase(key)
	case key > n.key:
		n.right = n.right.erase(key)
	default:
		switch {
		case n.left == nil:
			return n.right
		case n.right == nil:
			return n.left
		case n.left.prio > n.right.prio:
			n = rotateRight(n)
			n.right = n.right.erase(key)
		default:
			n = rotateLeft(n)
			n.left = n.left.erase(key)
		}
	}
	n.pull()
	return n
}
