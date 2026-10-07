package temporal

import "hash/fnv"

// treapKey identifies one adjacency slot: outgoing edges from a source object
// are keyed by (link type, target object); incoming edges by (link type,
// source object).
type treapKey struct {
	linkType LinkTypeID
	other    ObjectID
}

// treapNode is a node of a persistent (immutable), deterministic treap used
// as the adjacency index. Mutations return a new root; old roots stay valid
// forever, which is what lets long-running traversals keep reading a fixed
// historical version while writers commit new versions concurrently.
type treapNode struct {
	key         treapKey
	priority    uint64
	left, right *treapNode
}

func (a treapKey) less(b treapKey) bool {
	if a.linkType != b.linkType {
		return a.linkType < b.linkType
	}
	return a.other < b.other
}

// keyPriority is a pure function of the key. A deterministic priority makes
// equal histories produce equivalent index shapes, which keeps production
// store and naive-oracle comparisons reproducible.
func keyPriority(k treapKey) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(k.linkType))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(k.other))
	return h.Sum64()
}

func cloneWith(root *treapNode, left, right *treapNode) *treapNode {
	return &treapNode{key: root.key, priority: root.priority, left: left, right: right}
}

// rotateRight returns the tree after a right rotation at root.
func rotateRight(root *treapNode) *treapNode {
	pivot := root.left
	return cloneWith(pivot, pivot.left, cloneWith(root, pivot.right, root.right))
}

// rotateLeft returns the tree after a left rotation at root.
func rotateLeft(root *treapNode) *treapNode {
	pivot := root.right
	return cloneWith(pivot, cloneWith(root, root.left, pivot.left), pivot.right)
}

// treapInsert returns a new root containing key. Inserting an existing key is
// a no-op returning the same node (no allocation, no shape change).
func treapInsert(root *treapNode, key treapKey) *treapNode {
	if root == nil {
		return &treapNode{key: key, priority: keyPriority(key)}
	}
	switch {
	case key.less(root.key):
		node := cloneWith(root, treapInsert(root.left, key), root.right)
		if node.left.priority > node.priority {
			return rotateRight(node)
		}
		return node
	case root.key.less(key):
		node := cloneWith(root, root.left, treapInsert(root.right, key))
		if node.right.priority > node.priority {
			return rotateLeft(node)
		}
		return node
	default:
		return root
	}
}

// treapDelete returns a new root without key. A missing key leaves the tree
// unchanged.
func treapDelete(root *treapNode, key treapKey) *treapNode {
	if root == nil {
		return nil
	}
	switch {
	case key.less(root.key):
		return cloneWith(root, treapDelete(root.left, key), root.right)
	case root.key.less(key):
		return cloneWith(root, root.left, treapDelete(root.right, key))
	default:
		if root.left == nil {
			return root.right
		}
		if root.right == nil {
			return root.left
		}
		if root.left.priority > root.right.priority {
			node := rotateRight(root)
			return cloneWith(node, node.left, treapDelete(node.right, key))
		}
		node := rotateLeft(root)
		return cloneWith(node, treapDelete(node.left, key), node.right)
	}
}

// treapContains is an O(log n) membership probe.
func treapContains(root *treapNode, key treapKey) bool {
	for root != nil {
		switch {
		case key.less(root.key):
			root = root.left
		case root.key.less(key):
			root = root.right
		default:
			return true
		}
	}
	return false
}

// treapIter performs an in-order walk, stopping early if fn returns false.
func treapIter(root *treapNode, fn func(treapKey) bool) {
	if root == nil {
		return
	}
	treapIter(root.left, fn)
	if fn(root.key) {
		treapIter(root.right, fn)
	}
}
