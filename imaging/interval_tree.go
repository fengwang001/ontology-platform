package imaging

import "sort"

type interval struct {
	key      int
	end      int
	id       string
	seq      int64
	priority int64
	left     *intervalNode
	right    *intervalNode
	maxEnd   int
	size     int
}

type intervalNode = interval

type intervalTree struct {
	roots map[string]*intervalNode
	seq   int64
}

func newIntervalTree() *intervalTree {
	return &intervalTree{roots: make(map[string]*intervalNode)}
}

func intervalNodeMaxEnd(node *intervalNode) int {
	if node == nil {
		return 0
	}
	return node.maxEnd
}

func intervalNodeSize(node *intervalNode) int {
	if node == nil {
		return 0
	}
	return node.size
}

func intervalNodeRefresh(node *intervalNode) {
	node.maxEnd = max(node.end, intervalNodeMaxEnd(node.left), intervalNodeMaxEnd(node.right))
	node.size = 1 + intervalNodeSize(node.left) + intervalNodeSize(node.right)
}

func rotateIntervalRight(node *intervalNode) *intervalNode {
	left := node.left
	node.left = left.right
	left.right = node
	intervalNodeRefresh(node)
	intervalNodeRefresh(left)
	return left
}

func rotateIntervalLeft(node *intervalNode) *intervalNode {
	right := node.right
	node.right = right.left
	right.left = node
	intervalNodeRefresh(node)
	intervalNodeRefresh(right)
	return right
}

func (t *intervalTree) insert(partition string, item interval) *intervalNode {
	t.seq++
	item.seq = t.seq
	item.priority = intervalPriority(item.key, item.seq)
	t.roots[partition] = t.insertNode(t.roots[partition], item)
	return t.roots[partition]
}

func (t *intervalTree) restore(partition string, item interval) *intervalNode {
	t.roots[partition] = t.restoreNode(t.roots[partition], item)
	return t.roots[partition]
}

func (t *intervalTree) restoreNode(node *intervalNode, item interval) *intervalNode {
	if node == nil {
		item.left = nil
		item.right = nil
		item.maxEnd = item.end
		item.size = 1
		return &item
	}
	if item.key < node.key || (item.key == node.key && item.seq < node.seq) {
		node.left = t.restoreNode(node.left, item)
		if node.left.priority > node.priority {
			node = rotateIntervalRight(node)
		}
	} else {
		node.right = t.restoreNode(node.right, item)
		if node.right.priority > node.priority {
			node = rotateIntervalLeft(node)
		}
	}
	intervalNodeRefresh(node)
	return node
}

func intervalPriority(key int, seq int64) int64 {
	value := uint64(key) ^ uint64(seq)*0x9e3779b97f4a7c15
	value ^= value >> 30
	value *= 0xbf58476d1ce4e5b9
	value ^= value >> 27
	value *= 0x94d049bb133111eb
	value ^= value >> 31
	return int64(value >> 1)
}

func (t *intervalTree) insertNode(node *intervalNode, item interval) *intervalNode {
	if node == nil {
		item.left = nil
		item.right = nil
		item.maxEnd = item.end
		item.size = 1
		return &item
	}
	if item.key < node.key || (item.key == node.key && item.seq < node.seq) {
		node.left = t.insertNode(node.left, item)
		if node.left.priority > node.priority {
			node = rotateIntervalRight(node)
		}
	} else {
		node.right = t.insertNode(node.right, item)
		if node.right.priority > node.priority {
			node = rotateIntervalLeft(node)
		}
	}
	intervalNodeRefresh(node)
	return node
}

func (t *intervalTree) remove(partition string, key int, id string, seq int64) {
	t.roots[partition] = removeIntervalNode(t.roots[partition], key, id)
}

func removeIntervalNode(node *intervalNode, key int, id string) *intervalNode {
	if node == nil {
		return nil
	}
	if key < node.key {
		node.left = removeIntervalNode(node.left, key, id)
	} else if key > node.key {
		node.right = removeIntervalNode(node.right, key, id)
	} else if node.id != id && node.left != nil && containsInterval(node.left, key, id) {
		node.left = removeIntervalNode(node.left, key, id)
	} else {
		if node.id == id {
			return intervalMerge(node.left, node.right)
		}
		node.right = removeIntervalNode(node.right, key, id)
	}
	intervalNodeRefresh(node)
	return node
}

func containsInterval(node *intervalNode, key int, id string) bool {
	if node == nil {
		return false
	}
	if key < node.key {
		return containsInterval(node.left, key, id)
	}
	if key > node.key {
		return containsInterval(node.right, key, id)
	}
	return node.id == id || containsInterval(node.left, key, id) || containsInterval(node.right, key, id)
}

func intervalMerge(left, right *intervalNode) *intervalNode {
	if left == nil {
		return right
	}
	if right == nil {
		return left
	}
	if left.priority > right.priority {
		left.right = intervalMerge(left.right, right)
		intervalNodeRefresh(left)
		return left
	}
	right.left = intervalMerge(left, right.left)
	intervalNodeRefresh(right)
	return right
}

func (t *intervalTree) overlaps(partition string, start, end int, excludeID string) []string {
	var result []string
	var visit func(*intervalNode)
	visit = func(node *intervalNode) {
		if node == nil || start >= node.maxEnd {
			return
		}
		visit(node.left)
		if node.key < end && start < node.end && node.id != excludeID {
			result = append(result, node.id)
		}
		if node.key < end {
			visit(node.right)
		}
	}
	visit(t.roots[partition])
	sort.Strings(result)
	return result
}
