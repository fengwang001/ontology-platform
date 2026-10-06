package billing

import "sync/atomic"

// multiset 是按 int64 键维护重复次数的随机化 treap，
// 支持期望 O(log K) 的插入、删除与“第 rank 大”顺序统计查询。
//
// 节点 cnt 表示该键的重复份数，sub 为整棵子树的份数之和；
// 因而相同值各自独立占位，无需展开为多个节点。
// 优先级由全局原子计数器经 splitmix64 派生，无随机种子、
// 无需加锁，且树结构在任何机器上都可复现。
type multiset struct {
	root *tNode
}

type tNode struct {
	key   int64
	cnt   int
	sub   int
	prio  uint64
	left  *tNode
	right *tNode
}

var nodeCounter uint64

func nextPriority() uint64 {
	// splitmix64 后处理，把连续计数器映射成彼此独立似的优先级。
	z := atomic.AddUint64(&nodeCounter, 1)
	z += 0x9E3779B97F4A7C15
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

func newNode(key int64) *tNode {
	return &tNode{key: key, cnt: 1, sub: 1, prio: nextPriority()}
}

func (n *tNode) pull() {
	n.sub = n.cnt
	if n.left != nil {
		n.sub += n.left.sub
	}
	if n.right != nil {
		n.sub += n.right.sub
	}
}

// rotateRight 以 n 的左孩子为轴右旋，返回新子树根。
func rotateRight(n *tNode) *tNode {
	x := n.left
	n.left = x.right
	x.right = n
	n.pull()
	x.pull()
	return x
}

// rotateLeft 以 n 的右孩子为轴左旋，返回新子树根。
func rotateLeft(n *tNode) *tNode {
	x := n.right
	n.right = x.left
	x.left = n
	n.pull()
	x.pull()
	return x
}

func insert(root *tNode, node *tNode) *tNode {
	if root == nil {
		return node
	}
	switch {
	case node.key < root.key:
		root.left = insert(root.left, node)
		root.pull()
		if root.left.prio > root.prio {
			root = rotateRight(root)
		}
	case node.key > root.key:
		root.right = insert(root.right, node)
		root.pull()
		if root.right.prio > root.prio {
			root = rotateLeft(root)
		}
	default:
		root.cnt++
		root.pull()
	}
	return root
}

func (m *multiset) add(key int64) {
	m.root = insert(m.root, newNode(key))
}

func merge(left, right *tNode) *tNode {
	if left == nil {
		return right
	}
	if right == nil {
		return left
	}
	if left.prio > right.prio {
		left.right = merge(left.right, right)
		left.pull()
		return left
	}
	right.left = merge(left, right.left)
	right.pull()
	return right
}

func erase(root *tNode, key int64) *tNode {
	if root == nil {
		return nil
	}
	switch {
	case key < root.key:
		root.left = erase(root.left, key)
		root.pull()
	case key > root.key:
		root.right = erase(root.right, key)
		root.pull()
	default:
		root.cnt--
		if root.cnt > 0 {
			root.pull()
			return root
		}
		return merge(root.left, root.right)
	}
	return root
}

func (m *multiset) remove(key int64) {
	m.root = erase(m.root, key)
}

// kthLargest 返回从大到小第 rank 大的值（rank 从 1 起，按份数计数）。
// rank 必须落在 [1, size()] 内。
func (m *multiset) kthLargest(rank int) int64 {
	node := m.root
	for node != nil {
		rightCount := 0
		if node.right != nil {
			rightCount = node.right.sub
		}
		if rank <= rightCount {
			node = node.right
			continue
		}
		if rank <= rightCount+node.cnt {
			return node.key
		}
		rank -= rightCount + node.cnt
		node = node.left
	}
	// 调用方保证 rank 合法，走到这里说明多重集内部状态损坏。
	panic("billing: kthLargest rank out of range")
}

// height 返回树高，供测试做结构校验。
func (m *multiset) height() int {
	var h func(*tNode) int
	h = func(n *tNode) int {
		if n == nil {
			return 0
		}
		l := h(n.left)
		r := h(n.right)
		if r > l {
			return r + 1
		}
		return l + 1
	}
	return h(m.root)
}

func (m *multiset) size() int {
	if m.root == nil {
		return 0
	}
	return m.root.sub
}
