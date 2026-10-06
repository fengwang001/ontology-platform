package whiteboard

// node 是隐式键（按中序位置）随机平衡树（treap）的节点。
// 每个节点保存父指针，使名次查询可沿父链在 O(树高) 内完成。
type node struct {
	id     string
	left   *node
	right  *node
	parent *node
	size   int
	prio   uint64
}

type treap struct {
	root *node
}

func (t *treap) size() int { return t.root.subtreeSize() }

func (n *node) subtreeSize() int {
	if n == nil {
		return 0
	}
	return n.size
}

// pull 重算子树大小并修正子节点父指针。
func (n *node) pull() {
	n.size = 1 + n.left.subtreeSize() + n.right.subtreeSize()
	if n.left != nil {
		n.left.parent = n
	}
	if n.right != nil {
		n.right.parent = n
	}
}

func (n *node) refreshPath() {
	for p := n; p != nil; p = p.parent {
		p.size = 1 + p.left.subtreeSize() + p.right.subtreeSize()
	}
}

// rotateRight 以 n 为支点右旋。旋转后 n 的父指针指向新子树根 x，
// x 的父指针指向旋转前 n 的父节点（祖父位置），由调用方完成替换。
func rotateRight(n *node) *node {
	x := n.left
	n.left = x.right
	if x.right != nil {
		x.right.parent = n
	}
	x.right = n
	x.parent = n.parent
	n.parent = x
	n.pull()
	x.pull()
	return x
}

func rotateLeft(n *node) *node {
	x := n.right
	n.right = x.left
	if x.left != nil {
		x.left.parent = n
	}
	x.left = n
	x.parent = n.parent
	n.parent = x
	n.pull()
	x.pull()
	return x
}

// attachAtParent 用 sub 替换 old 在其父节点处的位置。
func (t *treap) attachAtParent(old, sub *node) {
	gp := sub.parent
	if gp == nil {
		t.root = sub
		sub.parent = nil
		return
	}
	if gp.left == old {
		gp.left = sub
	} else {
		gp.right = sub
	}
	sub.parent = gp
	gp.refreshPath()
}

// insertAt 把新节点插到 0 基位置 pos；调用方保证 0<=pos<=size。
func (t *treap) insertAt(pos int, x *node) {
	x.left, x.right, x.parent = nil, nil, nil
	x.size = 1
	if t.root == nil {
		t.root = x
		return
	}
	var parent *node
	cur := t.root
	dir := 1
	for cur != nil {
		parent = cur
		leftSize := cur.left.subtreeSize()
		if pos <= leftSize {
			cur = cur.left
			dir = 0
		} else {
			pos -= leftSize + 1
			cur = cur.right
			dir = 1
		}
	}
	if dir == 0 {
		parent.left = x
	} else {
		parent.right = x
	}
	x.parent = parent
	parent.refreshPath()
	// 按最小堆（prio 小者在上）旋转修复。
	for x.parent != nil && x.prio < x.parent.prio {
		p := x.parent
		beforeParent := p.parent
		var sub *node
		if p.left == x {
			sub = rotateRight(p)
		} else {
			sub = rotateLeft(p)
		}
		if beforeParent == nil {
			t.root = sub
			sub.parent = nil
		} else {
			if beforeParent.left == p {
				beforeParent.left = sub
			} else {
				beforeParent.right = sub
			}
			sub.parent = beforeParent
			beforeParent.refreshPath()
		}
	}
}

func (t *treap) pushBack(x *node) { t.insertAt(t.size(), x) }

// erase 摘除已在树中的节点 x。策略：按 treap 堆性质把 x 旋转到
// 叶子位置再摘除，保持其余节点优先级结构与父指针一致。
func (t *treap) erase(x *node) *node {
	for x.left != nil || x.right != nil {
		var sub *node
		switch {
		case x.left == nil:
			sub = rotateLeft(x)
		case x.right == nil || x.left.prio < x.right.prio:
			sub = rotateRight(x)
		default:
			sub = rotateLeft(x)
		}
		t.attachAtParent(x, sub)
	}
	p := x.parent
	if p != nil {
		if p.left == x {
			p.left = nil
		} else {
			p.right = nil
		}
		p.refreshPath()
	} else {
		t.root = nil
	}
	x.parent, x.left, x.right = nil, nil, nil
	x.size = 1
	return x
}

// rankOf 返回节点自底起的 1 基名次，沿父指针行走，复杂度 O(树高)。
func (t *treap) rankOf(x *node) int {
	r := x.left.subtreeSize() + 1
	for cur := x; cur.parent != nil; cur = cur.parent {
		p := cur.parent
		if p.right == cur {
			r += p.left.subtreeSize() + 1
		}
	}
	return r
}

// at 返回 1 基名次 k 上的节点，越界返回 nil。
func (t *treap) at(k int) *node {
	cur := t.root
	for cur != nil {
		leftSize := cur.left.subtreeSize()
		switch {
		case k == leftSize+1:
			return cur
		case k <= leftSize:
			cur = cur.left
		default:
			k -= leftSize + 1
			cur = cur.right
		}
	}
	return nil
}

// inorder 返回自底向上完整序列。
func (t *treap) inorder() []string {
	out := make([]string, 0, t.size())
	var walk func(*node)
	walk = func(n *node) {
		if n == nil {
			return
		}
		walk(n.left)
		out = append(out, n.id)
		walk(n.right)
	}
	walk(t.root)
	return out
}

// between 返回 1 基闭区间 [lo,hi] 内元素，自底向上。
func (t *treap) between(lo, hi int) []string {
	out := make([]string, 0, hi-lo+1)
	var walk func(*node, int)
	walk = func(n *node, base int) {
		if n == nil {
			return
		}
		leftSize := n.left.subtreeSize()
		rank := base + leftSize + 1
		if lo < rank {
			walk(n.left, base)
		}
		if rank >= lo && rank <= hi {
			out = append(out, n.id)
		}
		if hi > rank {
			walk(n.right, rank)
		}
	}
	walk(t.root, 0)
	return out
}

// next 返回中序后继（无则 nil）。
func (t *treap) next(x *node) *node {
	if x.right != nil {
		cur := x.right
		for cur.left != nil {
			cur = cur.left
		}
		return cur
	}
	cur := x
	for cur.parent != nil {
		p := cur.parent
		if p.left == cur {
			return p
		}
		cur = p
	}
	return nil
}

func newTreapNode(id string, prio uint64) *node {
	return &node{id: id, size: 1, prio: prio}
}
