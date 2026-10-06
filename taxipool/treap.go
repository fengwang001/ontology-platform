package taxipool

// 有序统计 treap：键为 (是否优先, 入池时刻, 司机标识)，维护子树大小，
// 支持 O(log n) 的插入、删除、秩查询（前方人数）。结构只保存当前在队节点，
// 因此查询开销与历史入池总数无关。优先级由键确定性哈希得出，重放结果一致。

type entryKey struct {
	priority bool
	ts       int64
	driverID string
}

func lessKey(a, b entryKey) bool {
	if a.priority != b.priority {
		return a.priority
	}
	if a.ts != b.ts {
		return a.ts < b.ts
	}
	return a.driverID < b.driverID
}

type node struct {
	key         entryKey
	prio        uint64
	size        int
	left, right *node
}

func sizeOf(n *node) int {
	if n == nil {
		return 0
	}
	return n.size
}

func (n *node) recalc() {
	n.size = 1 + sizeOf(n.left) + sizeOf(n.right)
}

func rotateRight(n *node) *node {
	l := n.left
	n.left = l.right
	l.right = n
	n.recalc()
	l.recalc()
	return l
}

func rotateLeft(n *node) *node {
	r := n.right
	n.right = r.left
	r.left = n
	n.recalc()
	r.recalc()
	return r
}

func insert(n *node, key entryKey) *node {
	if n == nil {
		return &node{key: key, prio: keyPriority(key), size: 1}
	}
	if lessKey(key, n.key) {
		n.left = insert(n.left, key)
		if n.left.prio < n.prio {
			n = rotateRight(n)
		}
	} else {
		n.right = insert(n.right, key)
		if n.right.prio < n.prio {
			n = rotateLeft(n)
		}
	}
	n.recalc()
	return n
}

func deleteNode(n *node, key entryKey) *node {
	if n == nil {
		return nil
	}
	switch {
	case lessKey(key, n.key):
		n.left = deleteNode(n.left, key)
	case lessKey(n.key, key):
		n.right = deleteNode(n.right, key)
	default:
		if n.left == nil {
			return n.right
		}
		if n.right == nil {
			return n.left
		}
		if n.left.prio < n.right.prio {
			n = rotateRight(n)
			n.right = deleteNode(n.right, key)
		} else {
			n = rotateLeft(n)
			n.left = deleteNode(n.left, key)
		}
	}
	n.recalc()
	return n
}

func minNode(n *node) *node {
	for n != nil && n.left != nil {
		n = n.left
	}
	return n
}

// rank 返回键 key 前方节点数（秩）。未找到返回 -1。
// visits 返回访问的节点数，用于验证查询开销上界。
func rank(n *node, key entryKey) (pos, visits int) {
	for n != nil {
		visits++
		switch {
		case lessKey(key, n.key):
			n = n.left
		case lessKey(n.key, key):
			pos += sizeOf(n.left) + 1
			n = n.right
		default:
			return pos + sizeOf(n.left), visits
		}
	}
	return -1, visits
}

func inorder(n *node, fn func(entryKey)) {
	if n == nil {
		return
	}
	inorder(n.left, fn)
	fn(n.key)
	inorder(n.right, fn)
}

// keyPriority 由键确定性导出 treap 堆优先级（FNV-1a + splitmix64 混合）。
func keyPriority(k entryKey) uint64 {
	h := uint64(14695981039346656037)
	for i := 0; i < len(k.driverID); i++ {
		h ^= uint64(k.driverID[i])
		h *= 1099511628211
	}
	h ^= uint64(k.ts)
	h *= 1099511628211
	if k.priority {
		h ^= 0x9E3779B97F4A7C15
	}
	h ^= h >> 30
	h *= 0xBF58476D1CE4E5B9
	h ^= h >> 27
	h *= 0x94D049BB133111EB
	h ^= h >> 31
	return h
}
