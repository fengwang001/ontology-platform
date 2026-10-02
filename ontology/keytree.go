package ontology

import "bytes"

type keyNode struct {
	key      []byte
	priority uint64
	left     *keyNode
	right    *keyNode
}

type keyTree struct {
	root *keyNode
}

func (t *keyTree) insert(key []byte) {
	t.root = t.insertNode(t.root, &keyNode{key: append([]byte(nil), key...), priority: keyPriority(key)})
}

func keyPriority(key []byte) uint64 {
	const offset64 = 14695981039346656037
	const prime64 = 1099511628211

	hash := uint64(offset64)
	for _, current := range key {
		hash ^= uint64(current)
		hash *= prime64
	}
	return hash
}

func (t *keyTree) insertNode(root, node *keyNode) *keyNode {
	if root == nil {
		return node
	}
	if bytes.Compare(node.key, root.key) < 0 {
		root.left = t.insertNode(root.left, node)
		if rankBefore(root.left, root) {
			root = t.rotateRight(root)
		}
	} else {
		root.right = t.insertNode(root.right, node)
		if rankBefore(root.right, root) {
			root = t.rotateLeft(root)
		}
	}
	return root
}

func rankBefore(left, right *keyNode) bool {
	if left.priority != right.priority {
		return left.priority > right.priority
	}
	return bytes.Compare(left.key, right.key) < 0
}

func (t *keyTree) rotateRight(node *keyNode) *keyNode {
	child := node.left
	node.left = child.right
	child.right = node
	return child
}

func (t *keyTree) rotateLeft(node *keyNode) *keyNode {
	child := node.right
	node.right = child.left
	child.left = node
	return child
}

func (t *keyTree) delete(key []byte) {
	t.root = t.deleteNode(t.root, key)
}

func (t *keyTree) deleteNode(root *keyNode, key []byte) *keyNode {
	if root == nil {
		return nil
	}
	switch cmp := bytes.Compare(key, root.key); {
	case cmp < 0:
		root.left = t.deleteNode(root.left, key)
	case cmp > 0:
		root.right = t.deleteNode(root.right, key)
	default:
		return t.merge(root.left, root.right)
	}
	return root
}

func (t *keyTree) merge(left, right *keyNode) *keyNode {
	if left == nil {
		return right
	}
	if right == nil {
		return left
	}
	if rankBefore(left, right) {
		left.right = t.merge(left.right, right)
		return left
	}
	right.left = t.merge(left, right.left)
	return right
}

func (t *keyTree) firstAfter(cursor []byte, limit int, visit func(key []byte) bool) int {
	if limit <= 0 {
		return 0
	}
	var stack []*keyNode
	node := t.root
	for node != nil {
		if bytes.Compare(node.key, cursor) > 0 {
			stack = append(stack, node)
			node = node.left
		} else {
			node = node.right
		}
	}

	processed := 0
	for processed < limit && len(stack) > 0 {
		node = stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if !visit(node.key) {
			return processed
		}
		processed++

		next := node.right
		for next != nil {
			stack = append(stack, next)
			next = next.left
		}
	}
	return processed
}
