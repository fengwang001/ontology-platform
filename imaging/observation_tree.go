package imaging

type countNode struct {
	key               int
	value             int
	priority          int64
	left, right       *countNode
	sum, prefix, size int
}

type countTree struct {
	root *countNode
	seq  int64
}

func countRefresh(node *countNode) {
	if node == nil {
		return
	}
	node.sum = countSum(node.left) + node.value + countSum(node.right)
	node.prefix = max(
		countPrefix(node.left),
		countSum(node.left)+node.value,
		countSum(node.left)+node.value+countPrefix(node.right),
	)
	node.size = 1 + countSize(node.left) + countSize(node.right)
}

func countSum(node *countNode) int {
	if node == nil {
		return 0
	}
	return node.sum
}

func countPrefix(node *countNode) int {
	if node == nil {
		return 0
	}
	return node.prefix
}

func countSize(node *countNode) int {
	if node == nil {
		return 0
	}
	return node.size
}

func (t *countTree) add(key, delta int) {
	if findCountNode(t.root, key) != nil {
		t.root = changeCountNode(t.root, key, delta)
		return
	}
	t.seq++
	t.root = addCountNode(t.root, key, delta, t.seq)
}

func findCountNode(node *countNode, key int) *countNode {
	for node != nil {
		if key == node.key {
			return node
		}
		if key < node.key {
			node = node.left
		} else {
			node = node.right
		}
	}
	return nil
}

func changeCountNode(node *countNode, key, delta int) *countNode {
	if node == nil {
		return nil
	}
	if key == node.key {
		node.value += delta
		countRefresh(node)
		return node
	}
	if key < node.key {
		node.left = changeCountNode(node.left, key, delta)
	} else {
		node.right = changeCountNode(node.right, key, delta)
	}
	countRefresh(node)
	return node
}

func addCountNode(node *countNode, key, delta int, priority int64) *countNode {
	if node == nil {
		return &countNode{key: key, value: delta, priority: priority, sum: delta, prefix: max(0, delta), size: 1}
	}
	if key < node.key {
		node.left = addCountNode(node.left, key, delta, priority)
		if node.left.priority > node.priority {
			left := node.left
			node.left = left.right
			left.right = node
			countRefresh(node)
			countRefresh(left)
			return left
		}
		countRefresh(node)
		return node
	}
	node.right = addCountNode(node.right, key, delta, priority)
	if node.right.priority > node.priority {
		right := node.right
		node.right = right.left
		right.left = node
		countRefresh(node)
		countRefresh(right)
		return right
	}
	countRefresh(node)
	return node
}

func (t *countTree) addRange(start, end, delta int) {
	t.add(start, delta)
	t.add(end, -delta)
}

func (t *countTree) maxOnRange(start, end int) int {
	base := prefixSum(t.root, start)
	_, result := rangeStats(t.root, start, end, base)
	return max(0, result)
}

func prefixSum(node *countNode, key int) int {
	total := 0
	for node != nil {
		if node.key < key {
			total += countSum(node.left) + node.value
			node = node.right
		} else {
			node = node.left
		}
	}
	return total
}

func rangeStats(node *countNode, low, high, current int) (int, int) {
	if node == nil {
		return current, current
	}
	if node.key < low {
		return rangeStats(node.right, low, high, current)
	}
	if node.key >= high {
		return rangeStats(node.left, low, high, current)
	}
	current, best := rangeStats(node.left, low, high, current)
	current += node.value
	best = max(best, current)
	current, rightBest := rangeStats(node.right, low, high, current)
	return current, max(best, rightBest)
}
