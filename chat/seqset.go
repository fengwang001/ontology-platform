package chat

// seqset is an order-statistics set of message sequence numbers backed by a
// treap. It provides insert, remove and countGreater in O(log n) expected
// time, which keeps Unread / UnreadMentions / MarkRead independent of the
// total channel history length.
//
// Priorities are derived deterministically from the key, so replaying the
// same operation sequence builds byte-identical trees.
type seqset struct {
	root *seqnode
}

type seqnode struct {
	key      int
	priority uint64
	size     int
	left     *seqnode
	right    *seqnode
}

// seqPriority is a splitmix64-style hash of the key, used as a deterministic
// treap priority.
func seqPriority(key int) uint64 {
	x := uint64(key)*0x9e3779b97f4a7c15 + 0x6a09e667f3bcc909
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}

func nodeSize(n *seqnode) int {
	if n == nil {
		return 0
	}
	return n.size
}

func (n *seqnode) recalc() {
	n.size = 1 + nodeSize(n.left) + nodeSize(n.right)
}

func rotateRight(n *seqnode) *seqnode {
	l := n.left
	n.left = l.right
	l.right = n
	n.recalc()
	l.recalc()
	return l
}

func rotateLeft(n *seqnode) *seqnode {
	r := n.right
	n.right = r.left
	r.left = n
	n.recalc()
	r.recalc()
	return r
}

// len reports the number of keys in the set. Nil sets are empty.
func (s *seqset) len() int {
	if s == nil {
		return 0
	}
	return nodeSize(s.root)
}

// contains reports whether key is present. Nil sets are empty.
func (s *seqset) contains(key int) bool {
	for n := s.root; n != nil; {
		switch {
		case key < n.key:
			n = n.left
		case key > n.key:
			n = n.right
		default:
			return true
		}
	}
	return false
}

// insert adds key; the caller must guarantee it is absent (set semantics).
func (s *seqset) insert(key int) {
	s.root = insertNode(s.root, key)
}

func insertNode(n *seqnode, key int) *seqnode {
	if n == nil {
		return &seqnode{key: key, priority: seqPriority(key), size: 1}
	}
	if key < n.key {
		n.left = insertNode(n.left, key)
		n.recalc()
		if n.left.priority > n.priority {
			return rotateRight(n)
		}
		return n
	}
	n.right = insertNode(n.right, key)
	n.recalc()
	if n.right.priority > n.priority {
		return rotateLeft(n)
	}
	return n
}

// remove deletes key; the caller must guarantee it is present.
func (s *seqset) remove(key int) {
	s.root = removeNode(s.root, key)
}

func removeNode(n *seqnode, key int) *seqnode {
	if n == nil {
		return nil
	}
	switch {
	case key < n.key:
		n.left = removeNode(n.left, key)
		n.recalc()
		return n
	case key > n.key:
		n.right = removeNode(n.right, key)
		n.recalc()
		return n
	}
	if n.left == nil {
		return n.right
	}
	if n.right == nil {
		return n.left
	}
	if n.left.priority > n.right.priority {
		n = rotateRight(n)
		n.right = removeNode(n.right, key)
	} else {
		n = rotateLeft(n)
		n.left = removeNode(n.left, key)
	}
	n.recalc()
	return n
}

// countGreater returns the number of keys strictly greater than key.
// Nil sets are empty.
func (s *seqset) countGreater(key int) int {
	if s == nil {
		return 0
	}
	count := 0
	for n := s.root; n != nil; {
		if key < n.key {
			count += 1 + nodeSize(n.right)
			n = n.left
		} else {
			n = n.right
		}
	}
	return count
}
