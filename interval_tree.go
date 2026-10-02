package broadphase

import "math/rand"

type intervalNode struct {
	lo     int64
	hi     int64
	id     int64
	maxHi  int64
	left   *intervalNode
	right  *intervalNode
	weight uint64
}

type intervalTree struct {
	root   *intervalNode
	random *rand.Rand
}

func newIntervalTree(seed int64) intervalTree {
	return intervalTree{random: rand.New(rand.NewSource(seed))}
}

func intervalMax(node *intervalNode) int64 {
	if node == nil {
		return 0
	}
	return node.maxHi
}

func updateIntervalNode(node *intervalNode) {
	node.maxHi = node.hi
	if intervalMax(node.left) > node.maxHi {
		node.maxHi = intervalMax(node.left)
	}
	if intervalMax(node.right) > node.maxHi {
		node.maxHi = intervalMax(node.right)
	}
}

func rotateIntervalRight(node *intervalNode) *intervalNode {
	child := node.left
	node.left = child.right
	child.right = node
	updateIntervalNode(node)
	updateIntervalNode(child)
	return child
}

func rotateIntervalLeft(node *intervalNode) *intervalNode {
	child := node.right
	node.right = child.left
	child.left = node
	updateIntervalNode(node)
	updateIntervalNode(child)
	return child
}

func insertIntervalNode(node, added *intervalNode) *intervalNode {
	if node == nil {
		return added
	}
	if added.lo < node.lo || (added.lo == node.lo && added.id < node.id) {
		node.left = insertIntervalNode(node.left, added)
		if node.left.weight > node.weight {
			node = rotateIntervalRight(node)
		}
	} else {
		node.right = insertIntervalNode(node.right, added)
		if node.right.weight > node.weight {
			node = rotateIntervalLeft(node)
		}
	}
	updateIntervalNode(node)
	return node
}

func eraseIntervalNode(node *intervalNode, lo, id int64) *intervalNode {
	if lo < node.lo || (lo == node.lo && id < node.id) {
		node.left = eraseIntervalNode(node.left, lo, id)
	} else if lo > node.lo || id > node.id {
		node.right = eraseIntervalNode(node.right, lo, id)
	} else {
		if node.left == nil {
			return node.right
		}
		if node.right == nil {
			return node.left
		}
		if node.left.weight > node.right.weight {
			node = rotateIntervalRight(node)
			node.right = eraseIntervalNode(node.right, lo, id)
		} else {
			node = rotateIntervalLeft(node)
			node.left = eraseIntervalNode(node.left, lo, id)
		}
	}
	updateIntervalNode(node)
	return node
}

func (tree *intervalTree) insert(lo, hi, id int64) {
	node := &intervalNode{
		lo:     lo,
		hi:     hi,
		id:     id,
		maxHi:  hi,
		weight: tree.random.Uint64(),
	}
	tree.root = insertIntervalNode(tree.root, node)
}

func (tree *intervalTree) erase(lo, id int64) {
	tree.root = eraseIntervalNode(tree.root, lo, id)
}

func visitOverlapping(node *intervalNode, query Box, visit func(int64)) {
	if node == nil || node.maxHi <= query.LX {
		return
	}
	visitOverlapping(node.left, query, visit)
	if node.lo < query.HX && node.hi > query.LX {
		visit(node.id)
	}
	if node.lo < query.HX {
		visitOverlapping(node.right, query, visit)
	}
}

func (tree *intervalTree) overlapsX(query Box, visit func(int64)) {
	visitOverlapping(tree.root, query, visit)
}
