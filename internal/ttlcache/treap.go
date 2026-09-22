package ttlcache

import "container/list"

// tnode 是驱逐索引（treap）的节点，与 LRU 链表中的元素一一对应。
//
// 中序键为 (writeAt, touch)：writeAt 小者在前；writeAt 并列时 touch 小者
// （即最近被触碰、离 LRU 队首最近者）在前——与 evict 的并列取舍规则一致。
// prio 是伪随机堆优先级，保证树的期望深度为 O(log n)。
// minExp 维护子树内最小 expireAt，使"中序第一个已过期节点"可沿单条
// 根到叶路径找到，无需全量扫描。
type tnode struct {
	el      *list.Element
	writeAt int64
	touch   int64
	exp     int64
	minExp  int64
	prio    uint64
	left    *tnode
	right   *tnode
}

// lessNode 比较中序键 (writeAt, touch)。
func lessNode(a, b *tnode) bool {
	if a.writeAt != b.writeAt {
		return a.writeAt < b.writeAt
	}
	return a.touch < b.touch
}

// fix 在子树结构变化后重算节点的 minExp。
func fix(n *tnode) {
	m := n.exp
	if n.left != nil && n.left.minExp < m {
		m = n.left.minExp
	}
	if n.right != nil && n.right.minExp < m {
		m = n.right.minExp
	}
	n.minExp = m
}

func rotateRight(root *tnode) *tnode {
	l := root.left
	root.left = l.right
	l.right = root
	fix(root)
	fix(l)
	return l
}

func rotateLeft(root *tnode) *tnode {
	r := root.right
	root.right = r.left
	r.left = root
	fix(root)
	fix(r)
	return r
}

// tInsert 插入节点 n；调用前 n.left、n.right 必须为 nil 且 n.minExp == n.exp。
func tInsert(root, n *tnode) *tnode {
	if root == nil {
		return n
	}
	if lessNode(n, root) {
		root.left = tInsert(root.left, n)
		if root.left.prio < root.prio {
			root = rotateRight(root)
		}
	} else {
		root.right = tInsert(root.right, n)
		if root.right.prio < root.prio {
			root = rotateLeft(root)
		}
	}
	fix(root)
	return root
}

// tMerge 合并两棵堆，l 的所有中序键均小于 r 的。
func tMerge(l, r *tnode) *tnode {
	if l == nil {
		return r
	}
	if r == nil {
		return l
	}
	if l.prio < r.prio {
		l.right = tMerge(l.right, r)
		fix(l)
		return l
	}
	r.left = tMerge(l, r.left)
	fix(r)
	return r
}

// tDelete 从树中摘除节点 n（按指针定位，键唯一）。被摘除节点的
// left/right 指针不会被清理，复用前必须重置。
func tDelete(root, n *tnode) *tnode {
	if root == nil {
		return nil
	}
	if root == n {
		return tMerge(root.left, root.right)
	}
	if lessNode(n, root) {
		root.left = tDelete(root.left, n)
	} else {
		root.right = tDelete(root.right, n)
	}
	fix(root)
	return root
}

// nextPrio 用 xorshift64 生成下一个堆优先级；种子固定，结构可复现。
func (c *Cache) nextPrio() uint64 {
	x := c.seed
	x ^= x << 13
	x ^= x >> 7
	x ^= x << 17
	c.seed = x
	return x
}

// findVictim 返回已过期项中 (writeAt, touch) 最小者，即写入时刻最早、
// 并列时最近使用的项；没有已过期项时返回 nil。只沿一条树路径下行，
// 途经的节点数记入 c.lastEvictExamined。
func (c *Cache) findVictim() *tnode {
	now := c.now()
	examined := 0
	n := c.root
	for n != nil {
		examined++
		if n.left != nil && n.left.minExp <= now {
			n = n.left
			continue
		}
		if n.exp <= now {
			break
		}
		n = n.right
	}
	c.lastEvictExamined = examined
	return n
}
