package broadphase

import "math/rand"

const (
	endpointHi = 0
	endpointLo = 1
)

type endpointKey struct {
	x    int64
	kind uint8
	id   int64
}

type endpointNode struct {
	key    endpointKey
	left   *endpointNode
	right  *endpointNode
	size   int
	weight uint64
}

type endpointTree struct {
	root   *endpointNode
	random *rand.Rand
}

func newEndpointTree(seed int64) endpointTree {
	return endpointTree{random: rand.New(rand.NewSource(seed))}
}

func compareEndpoint(left, right endpointKey) int {
	switch {
	case left.x < right.x:
		return -1
	case left.x > right.x:
		return 1
	}
	switch {
	case left.kind < right.kind:
		return -1
	case left.kind > right.kind:
		return 1
	}
	switch {
	case left.id < right.id:
		return -1
	case left.id > right.id:
		return 1
	}
	return 0
}

func updateEndpointSize(node *endpointNode) {
	if node == nil {
		return
	}
	node.size = 1 + endpointSize(node.left) + endpointSize(node.right)
}

func endpointSize(node *endpointNode) int {
	if node == nil {
		return 0
	}
	return node.size
}

func rotateEndpointRight(node *endpointNode) *endpointNode {
	child := node.left
	node.left = child.right
	child.right = node
	updateEndpointSize(node)
	updateEndpointSize(child)
	return child
}

func rotateEndpointLeft(node *endpointNode) *endpointNode {
	child := node.right
	node.right = child.left
	child.left = node
	updateEndpointSize(node)
	updateEndpointSize(child)
	return child
}

func insertEndpointNode(node *endpointNode, added *endpointNode) *endpointNode {
	if node == nil {
		return added
	}
	if compareEndpoint(added.key, node.key) < 0 {
		node.left = insertEndpointNode(node.left, added)
		if node.left.weight > node.weight {
			node = rotateEndpointRight(node)
		}
	} else {
		node.right = insertEndpointNode(node.right, added)
		if node.right.weight > node.weight {
			node = rotateEndpointLeft(node)
		}
	}
	updateEndpointSize(node)
	return node
}

func eraseEndpointNode(node *endpointNode, key endpointKey) *endpointNode {
	order := compareEndpoint(key, node.key)
	if order < 0 {
		node.left = eraseEndpointNode(node.left, key)
	} else if order > 0 {
		node.right = eraseEndpointNode(node.right, key)
	} else {
		if node.left == nil {
			return node.right
		}
		if node.right == nil {
			return node.left
		}
		if node.left.weight > node.right.weight {
			node = rotateEndpointRight(node)
			node.right = eraseEndpointNode(node.right, key)
		} else {
			node = rotateEndpointLeft(node)
			node.left = eraseEndpointNode(node.left, key)
		}
	}
	updateEndpointSize(node)
	return node
}

func (tree *endpointTree) insert(key endpointKey) *endpointNode {
	node := &endpointNode{
		key:    key,
		size:   1,
		weight: tree.random.Uint64(),
	}
	tree.root = insertEndpointNode(tree.root, node)
	return node
}

func (tree *endpointTree) erase(key endpointKey) {
	tree.root = eraseEndpointNode(tree.root, key)
}

func (tree *endpointTree) countLess(key endpointKey) int {
	count := 0
	for node := tree.root; node != nil; {
		order := compareEndpoint(node.key, key)
		if order < 0 {
			count += endpointSize(node.left) + 1
			node = node.right
		} else {
			node = node.left
		}
	}
	return count
}

func rangeEndpointNodes(node *endpointNode, low, high endpointKey, visit func(*endpointNode) bool) bool {
	if node == nil {
		return true
	}
	if compareEndpoint(node.key, low) < 0 {
		return rangeEndpointNodes(node.right, low, high, visit)
	}
	if compareEndpoint(node.key, high) > 0 {
		return rangeEndpointNodes(node.left, low, high, visit)
	}
	if !rangeEndpointNodes(node.left, low, high, visit) {
		return false
	}
	if !visit(node) {
		return false
	}
	return rangeEndpointNodes(node.right, low, high, visit)
}

func (tree *endpointTree) between(low, high endpointKey, visit func(endpointKey) bool) {
	rangeEndpointNodes(tree.root, low, high, func(node *endpointNode) bool {
		return visit(node.key)
	})
}
