package compaction

type intervalNode struct {
	file       *File
	left       *intervalNode
	right      *intervalNode
	parent     *intervalNode
	height     int
	subtreeMax []byte
}

type intervalTree struct {
	root        *intervalNode
	lastVisited int
}

func compareKeys(left, right []byte) int {
	limit := len(left)
	if len(right) < limit {
		limit = len(right)
	}
	for index := 0; index < limit; index++ {
		if left[index] != right[index] {
			if left[index] < right[index] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(left) < len(right):
		return -1
	case len(left) > len(right):
		return 1
	default:
		return 0
	}
}

func maxKey(keys ...[]byte) []byte {
	var result []byte
	for _, key := range keys {
		if result == nil || compareKeys(key, result) > 0 {
			result = key
		}
	}
	return result
}

func nodeHeight(node *intervalNode) int {
	if node == nil {
		return -1
	}
	return node.height
}

func (node *intervalNode) refresh() {
	node.height = nodeHeight(node.left) + 1
	if rightHeight := nodeHeight(node.right); rightHeight+1 > node.height {
		node.height = rightHeight + 1
	}
	node.subtreeMax = maxKey(node.file.MaxKey, subtreeMax(node.left), subtreeMax(node.right))
}

func subtreeMax(node *intervalNode) []byte {
	if node == nil {
		return nil
	}
	return node.subtreeMax
}

func (tree *intervalTree) insert(file *File) {
	node := &intervalNode{file: file, height: 0, subtreeMax: file.MaxKey}
	if tree.root == nil {
		tree.root = node
		return
	}
	parent := tree.root
	for {
		comparison := nodeOrder(file, parent.file)
		if comparison < 0 {
			if parent.left == nil {
				parent.left = node
				node.parent = parent
				break
			}
			parent = parent.left
		} else {
			if parent.right == nil {
				parent.right = node
				node.parent = parent
				break
			}
			parent = parent.right
		}
	}
	tree.rebalanceAfter(node)
}

func (tree *intervalTree) remove(file *File) {
	node := tree.findNode(file)
	if node == nil {
		return
	}
	if node.left != nil && node.right != nil {
		successor := minimumNode(node.right)
		tree.remove(successor.file)
		successor.parent = node.parent
		successor.left = node.left
		successor.right = node.right
		if successor.left != nil {
			successor.left.parent = successor
		}
		if successor.right != nil {
			successor.right.parent = successor
		}
		tree.replaceChild(node.parent, node, successor)
		successor.refresh()
		tree.rebalanceAfter(successor)
		return
	}
	child := node.left
	if child == nil {
		child = node.right
	}
	parent := node.parent
	tree.replaceChild(parent, node, child)
	if child != nil {
		child.parent = parent
	}
	tree.rebalanceAfter(parent)
}

func (tree *intervalTree) findNode(file *File) *intervalNode {
	node := tree.root
	for node != nil {
		comparison := nodeOrder(file, node.file)
		if comparison == 0 {
			return node
		}
		if comparison < 0 {
			node = node.left
		} else {
			node = node.right
		}
	}
	return nil
}

func nodeOrder(left, right *File) int {
	if comparison := compareKeys(left.MinKey, right.MinKey); comparison != 0 {
		return comparison
	}
	if comparison := compareKeys(left.MaxKey, right.MaxKey); comparison != 0 {
		return comparison
	}
	if left.ID < right.ID {
		return -1
	}
	if left.ID > right.ID {
		return 1
	}
	return 0
}

func (tree *intervalTree) replaceChild(parent, old, replacement *intervalNode) {
	if parent == nil {
		tree.root = replacement
	} else if parent.left == old {
		parent.left = replacement
	} else {
		parent.right = replacement
	}
	if replacement != nil {
		replacement.parent = parent
	}
}

func minimumNode(node *intervalNode) *intervalNode {
	for node.left != nil {
		node = node.left
	}
	return node
}

func (tree *intervalTree) rotateLeft(node *intervalNode) *intervalNode {
	pivot := node.right
	parent := node.parent
	node.right = pivot.left
	if pivot.left != nil {
		pivot.left.parent = node
	}
	pivot.left = node
	node.parent = pivot
	pivot.parent = parent
	tree.replaceChild(parent, node, pivot)
	node.refresh()
	pivot.refresh()
	return pivot
}

func (tree *intervalTree) rotateRight(node *intervalNode) *intervalNode {
	pivot := node.left
	parent := node.parent
	node.left = pivot.right
	if pivot.right != nil {
		pivot.right.parent = node
	}
	pivot.right = node
	node.parent = pivot
	pivot.parent = parent
	tree.replaceChild(parent, node, pivot)
	node.refresh()
	pivot.refresh()
	return pivot
}

func (tree *intervalTree) rebalanceAfter(node *intervalNode) {
	for node != nil {
		oldHeight := node.height
		node.refresh()
		balance := nodeHeight(node.left) - nodeHeight(node.right)
		if balance > 1 {
			if nodeHeight(node.left.right) > nodeHeight(node.left.left) {
				node.left = tree.rotateLeft(node.left)
			}
			node = tree.rotateRight(node)
		} else if balance < -1 {
			if nodeHeight(node.right.left) > nodeHeight(node.right.right) {
				node.right = tree.rotateRight(node.right)
			}
			node = tree.rotateLeft(node)
		}
		if node.parent == nil {
			tree.root = node
		}
		newHeight := node.height
		node = node.parent
		if oldHeight == newHeight && node == nil {
			break
		}
	}
}

func (tree *intervalTree) overlaps(minKey, maxKey []byte) []*File {
	var result []*File
	visited := 0
	var visit func(*intervalNode)
	visit = func(node *intervalNode) {
		if node == nil || compareKeys(subtreeMax(node), minKey) < 0 {
			return
		}
		visited++
		visit(node.left)
		if compareKeys(node.file.MinKey, maxKey) <= 0 && compareKeys(minKey, node.file.MaxKey) <= 0 {
			result = append(result, node.file)
		}
		if compareKeys(node.file.MinKey, maxKey) <= 0 {
			visit(node.right)
		}
	}
	visit(tree.root)
	tree.lastVisited = visited
	return result
}

func (tree *intervalTree) overlapsWithVisitCount(minKey, maxKey []byte) ([]*File, int) {
	result := tree.overlaps(minKey, maxKey)
	return result, tree.lastVisited
}

func (tree *intervalTree) height() int {
	return nodeHeight(tree.root) + 1
}
