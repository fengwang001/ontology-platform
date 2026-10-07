package whiteboard

import "hash/fnv"

// node 是隐式笛卡尔树（implicit treap）的节点。
// 中序遍历顺序即元素自底到顶的全序；子树 size 支撑名次计算与按名次分裂。
// parent 指针使任意节点的名次可在 O(深度) 内求得，无需从根按键查找。
type node struct {
	id     string
	prio   uint64
	size   int
	left   *node
	right  *node
	parent *node
}

// treap 提供分裂/合并/名次等原语。
// visits 统计原语访问的节点数，用于以确定性的方式验证
// Rank 与单元素 Reorder 的开销为 O(log n) 而非 O(n)（见 perf_test.go）。
type treap struct {
	visits int64
}

func nodeSize(n *node) int {
	if n == nil {
		return 0
	}
	return n.size
}

// priority 由标识散列确定：确定性、可复现。FNV-1a 对相似短标识的
// 高位区分度不足，再经 splitmix64 终末混合（雪崩效应）打散，
// 使任意命名习惯下期望树高仍为 O(log n)。
func priority(id string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(id))
	x := h.Sum64()
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	x ^= x >> 31
	return x
}

// higher 报告 a 的堆优先级是否高于 b（小顶堆，数值小者优先）。
func higher(a, b *node) bool {
	if a.prio != b.prio {
		return a.prio < b.prio
	}
	return a.id < b.id
}

func (t *treap) update(n *node) {
	n.size = 1 + nodeSize(n.left) + nodeSize(n.right)
	if n.left != nil {
		n.left.parent = n
	}
	if n.right != nil {
		n.right.parent = n
	}
}

// split 把以 n 为根的树按中序前 k 个节点分裂为 (a, b)。
// 调用方须把返回的两棵子树根的 parent 置空。
func (t *treap) split(n *node, k int) (a, b *node) {
	if n == nil {
		return nil, nil
	}
	t.visits++
	if nodeSize(n.left) >= k {
		a, b = t.split(n.left, k)
		n.left = b
		t.update(n)
		return a, n
	}
	a, b = t.split(n.right, k-nodeSize(n.left)-1)
	n.right = a
	t.update(n)
	return n, b
}

// merge 合并两棵树，a 的中序序列整体在 b 之前。
// 调用方须把返回的树根 parent 置空。
func (t *treap) merge(a, b *node) *node {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	t.visits++
	if higher(a, b) {
		a.right = t.merge(a.right, b)
		t.update(a)
		return a
	}
	b.left = t.merge(a, b.left)
	t.update(b)
	return b
}

// rank 返回节点 n 在整棵树中的中序名次（自底起，从 0 开始），O(树高)。
func (t *treap) rank(n *node) int {
	r := nodeSize(n.left)
	for n.parent != nil {
		t.visits++
		if n == n.parent.right {
			r += nodeSize(n.parent.left) + 1
		}
		n = n.parent
	}
	return r
}

// kth 返回中序名次为 k 的节点，O(树高)。
func (t *treap) kth(root *node, k int) *node {
	for {
		t.visits++
		ls := nodeSize(root.left)
		switch {
		case k < ls:
			root = root.left
		case k == ls:
			return root
		default:
			k -= ls + 1
			root = root.right
		}
	}
}
