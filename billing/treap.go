package billing

// treap 是按有效值键控、带子树大小与多重计数的随机平衡二叉搜索树。
// 它实现“有序多重集”，支持插入一份、删除一份、以及查询第 rank 大的值。
// 不同槽位的相同有效值各自独立占位（重数 count）。
//
// 期望值复杂度（不同有效值个数 D，总份数 K）：
//
//	插入/删除/第 k 大：O(log D)；
//	多重计数保证 K 份采样最多 D 个节点，且 D <= K。
type treap struct {
	root           *treapNode
	lastWriteSteps int // 最近一次写操作（insert/remove）沿树访问节点数；写锁独占，安全
	rngState       uint64
}

type treapNode struct {
	key   int64
	count int // 该值的重数（并列值各自占位）
	size  int // 子树总份数
	prio  uint64
	left  *treapNode
	right *treapNode
}

func newTreap() *treap {
	// 非零种子，避免 splitmix64 退化到全零。
	return &treap{rngState: 0x9E3779B97F4A7C15}
}

// total 返回多重集中的总份数（有效槽位数 K）。
func (t *treap) total() int {
	if t.root == nil {
		return 0
	}
	return t.root.size
}

// nextPrio 用 splitmix64 产生确定性伪随机优先级。
func (t *treap) nextPrio() uint64 {
	t.rngState += 0x9E3779B97F4A7C15
	z := t.rngState
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

func nodeSize(n *treapNode) int {
	if n == nil {
		return 0
	}
	return n.size
}

// pull 由子节点重算子树份数。
func (n *treapNode) pull() {
	n.size = n.count + nodeSize(n.left) + nodeSize(n.right)
}

// rotateRight 以 n 为轴右旋，返回旋转后的子树根。
func rotateRight(n *treapNode) *treapNode {
	child := n.left
	n.left = child.right
	child.right = n
	n.pull()
	child.pull()
	return child
}

// rotateLeft 以 n 为轴左旋，返回旋转后的子树根。
func rotateLeft(n *treapNode) *treapNode {
	child := n.right
	n.right = child.left
	child.left = n
	n.pull()
	child.pull()
	return child
}

// insert 向多重集加入一份 key（key 已存在则重数 +1）。
func (t *treap) insert(key int64) {
	steps := 0
	t.root = t.insertNode(t.root, key, &steps)
	t.lastWriteSteps = steps
}

func (t *treap) insertNode(n *treapNode, key int64, steps *int) *treapNode {
	if n == nil {
		if steps != nil {
			*steps++
		}
		return &treapNode{key: key, count: 1, size: 1, prio: t.nextPrio()}
	}
	if steps != nil {
		*steps++
	}
	switch {
	case key < n.key:
		n.left = t.insertNode(n.left, key, steps)
		if n.left.prio > n.prio {
			n = rotateRight(n)
		}
	case key > n.key:
		n.right = t.insertNode(n.right, key, steps)
		if n.right.prio > n.prio {
			n = rotateLeft(n)
		}
	default:
		n.count++
	}
	n.pull()
	return n
}

// remove 从多重集移除一份 key；最后一份移除时删除节点。
// 调用方须保证 key 存在。
func (t *treap) remove(key int64) {
	steps := 0
	t.root = t.removeNode(t.root, key, &steps)
	t.lastWriteSteps = steps
}

func (t *treap) removeNode(n *treapNode, key int64, steps *int) *treapNode {
	if n == nil {
		return nil
	}
	if steps != nil {
		*steps++
	}
	switch {
	case key < n.key:
		n.left = t.removeNode(n.left, key, steps)
	case key > n.key:
		n.right = t.removeNode(n.right, key, steps)
	default:
		if n.count > 1 {
			n.count--
			n.pull()
			return n
		}
		switch {
		case n.left == nil:
			return n.right
		case n.right == nil:
			return n.left
		default:
			if n.left.prio > n.right.prio {
				n = rotateRight(n)
				n.right = t.removeNode(n.right, key, steps)
			} else {
				n = rotateLeft(n)
				n.left = t.removeNode(n.left, key, steps)
			}
		}
	}
	n.pull()
	return n
}

// kthLargest 返回第 rank 大的值；rank 从 1 开始（1 表示最大值）。
// 调用方须保证 1 <= rank <= total()。
// steps 非 nil 时累计沿树访问的节点数；函数本身不修改任何状态，
// 因而可在读锁保护下并发调用。
func (t *treap) kthLargest(rank int, steps *int) int64 {
	n := t.root
	for n != nil {
		if steps != nil {
			*steps++
		}
		rightSize := nodeSize(n.right)
		if rank <= rightSize {
			n = n.right
		} else if rank <= rightSize+n.count {
			return n.key
		} else {
			rank -= rightSize + n.count
			n = n.left
		}
	}
	return 0
}
