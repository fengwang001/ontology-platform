package intervalindex

// node is one treap vertex. The treap is ordered by (lo, id); hi is payload.
// Each subtree aggregates minLo, maxLo and maxHi so query traversals can prune
// whole subtrees without touching the intervals inside them.
type node struct {
	lo, hi, id int64
	priority   uint64
	left       *node
	right      *node

	size  int
	minLo int64
	maxLo int64
	maxHi int64
}

func (n *node) pull() {
	n.size = 1
	n.minLo = n.lo
	n.maxLo = n.lo
	n.maxHi = n.hi
	if n.left != nil {
		n.size += n.left.size
		n.minLo = n.left.minLo
		if n.left.maxLo > n.maxLo {
			n.maxLo = n.left.maxLo
		}
		if n.left.maxHi > n.maxHi {
			n.maxHi = n.left.maxHi
		}
	}
	if n.right != nil {
		n.size += n.right.size
		n.maxLo = n.right.maxLo
		if n.right.minLo < n.minLo {
			n.minLo = n.right.minLo
		}
		if n.right.maxHi > n.maxHi {
			n.maxHi = n.right.maxHi
		}
	}
}

// less keys the treap by (lo, id) ascending; hi does not participate.
func less(loA, idA, loB, idB int64) bool {
	return loA < loB || (loA == loB && idA < idB)
}

// rotateRight returns the tree after moving n's left child into n's place.
func rotateRight(n *node) *node {
	child := n.left
	n.left = child.right
	child.right = n
	n.pull()
	child.pull()
	return child
}

// rotateLeft returns the tree after moving n's right child into n's place.
func rotateLeft(n *node) *node {
	child := n.right
	n.right = child.left
	child.left = n
	n.pull()
	child.pull()
	return child
}

// insert expects (lo, id) to be absent from the tree.
func insert(root *node, n *node) *node {
	if root == nil {
		n.pull()
		return n
	}
	if less(n.lo, n.id, root.lo, root.id) {
		root.left = insert(root.left, n)
		if root.left.priority > root.priority {
			root = rotateRight(root)
		}
	} else {
		root.right = insert(root.right, n)
		if root.right.priority > root.priority {
			root = rotateLeft(root)
		}
	}
	root.pull()
	return root
}

// erase removes the unique vertex keyed by (lo, id).
func erase(root *node, lo, id int64) *node {
	if root == nil {
		return nil
	}
	switch {
	case less(lo, id, root.lo, root.id):
		root.left = erase(root.left, lo, id)
	case less(root.lo, root.id, lo, id):
		root.right = erase(root.right, lo, id)
	default:
		switch {
		case root.left == nil:
			return root.right
		case root.right == nil:
			return root.left
		default:
			if root.left.priority > root.right.priority {
				root = rotateRight(root)
				root.right = erase(root.right, lo, id)
			} else {
				root = rotateLeft(root)
				root.left = erase(root.left, lo, id)
			}
		}
	}
	root.pull()
	return root
}
