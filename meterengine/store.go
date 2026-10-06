package meterengine

import "math/rand"

// treap 按读数时刻排序，支持 O(log n) 的插入、删除与前驱后继查找。
// 登记/替换/删除只需沿一条根叶路径定位节点及其邻居，
// 开销不随该电表读数总数线性增长。
type treap struct {
	root *treapNode
	rng  *rand.Rand
}

type treapNode struct {
	reading  *Reading
	left     *treapNode
	right    *treapNode
	priority uint64
	size     int
}

func newTreap() *treap {
	return &treap{rng: rand.New(rand.NewSource(1))}
}

func (n *treapNode) pull() {
	n.size = 1
	if n.left != nil {
		n.size += n.left.size
	}
	if n.right != nil {
		n.size += n.right.size
	}
}

// neighbors 返回 time 严格小于/大于它的最近读数。
func (t *treap) neighbors(time int64) (pred, succ *Reading) {
	for n := t.root; n != nil; {
		switch {
		case time < n.reading.time:
			succ = n.reading
			n = n.left
		case time > n.reading.time:
			pred = n.reading
			n = n.right
		default:
			p, s := rightmostReading(n.left), leftmostReading(n.right)
			if p != nil {
				pred = p
			}
			if s != nil {
				succ = s
			}
			return pred, succ
		}
	}
	return pred, succ
}

func rightmostReading(n *treapNode) *Reading {
	if n == nil {
		return nil
	}
	for n.right != nil {
		n = n.right
	}
	return n.reading
}

func leftmostReading(n *treapNode) *Reading {
	if n == nil {
		return nil
	}
	for n.left != nil {
		n = n.left
	}
	return n.reading
}

func (t *treap) insert(r *Reading) (pred, succ, existed *Reading, ok bool) {
	if cur := t.find(r.time); cur != nil {
		p, s := t.neighbors(r.time)
		return p, s, cur, false
	}
	p, s := t.neighbors(r.time)
	node := &treapNode{reading: r, priority: t.rng.Uint64(), size: 1}
	t.root = treapInsert(t.root, node)
	return p, s, nil, true
}

// treapInsert 按键序递归插入，并按优先级旋转维护堆性质。
func treapInsert(root, node *treapNode) *treapNode {
	if root == nil {
		return node
	}
	if node.reading.time < root.reading.time {
		root.left = treapInsert(root.left, node)
		root.pull()
		if root.left.priority > root.priority {
			return rotateRight(root)
		}
	} else {
		root.right = treapInsert(root.right, node)
		root.pull()
		if root.right.priority > root.priority {
			return rotateLeft(root)
		}
	}
	return root
}

func rotateRight(n *treapNode) *treapNode {
	x := n.left
	n.left = x.right
	x.right = n
	n.pull()
	x.pull()
	return x
}

func rotateLeft(n *treapNode) *treapNode {
	x := n.right
	n.right = x.left
	x.left = n
	n.pull()
	x.pull()
	return x
}

func readingOf(n *treapNode) *Reading {
	if n == nil {
		return nil
	}
	return n.reading
}

// split 把 root 拆为时刻 < time 与时刻 >= time 两棵树。
func split(root *treapNode, time int64) (left, right *treapNode) {
	if root == nil {
		return nil, nil
	}
	if root.reading.time < time {
		l, r := split(root.right, time)
		root.right = l
		root.pull()
		return root, r
	}
	l, r := split(root.left, time)
	root.left = r
	root.pull()
	return l, root
}

func merge(left, right *treapNode) *treapNode {
	if left == nil {
		return right
	}
	if right == nil {
		return left
	}
	if left.priority > right.priority {
		left.right = merge(left.right, right)
		left.pull()
		return left
	}
	right.left = merge(left, right.left)
	right.pull()
	return right
}

func (t *treap) deleteAt(time int64) (removed, pred, succ *Reading, ok bool) {
	cur := t.find(time)
	if cur == nil {
		return nil, nil, nil, false
	}
	p, s := t.neighbors(time)
	l, rest := split(t.root, time)
	_, r := split(rest, time+1)
	t.root = merge(l, r)
	return cur, p, s, true
}

func (t *treap) find(time int64) *Reading {
	for n := t.root; n != nil; {
		switch {
		case time < n.reading.time:
			n = n.left
		case time > n.reading.time:
			n = n.right
		default:
			return n.reading
		}
	}
	return nil
}

func (t *treap) latest() *Reading {
	n := t.root
	if n == nil {
		return nil
	}
	for n.right != nil {
		n = n.right
	}
	return n.reading
}

// iterate 按时刻升序访问，返回 false 提前停止。
func (t *treap) iterate(fn func(*Reading) bool) {
	var walk func(*treapNode) bool
	walk = func(n *treapNode) bool {
		if n == nil {
			return true
		}
		return walk(n.left) && fn(n.reading) && walk(n.right)
	}
	walk(t.root)
}

// iterateRange 仅按序访问时刻位于 [lo,hi] 的读数。
// 访问节点数为 O(k + log n)（k 为区间内读数数），
// 不随区间之外的读数数量增长。
func (t *treap) iterateRange(lo, hi int64, fn func(*Reading) bool) bool {
	var walk func(*treapNode) bool
	walk = func(n *treapNode) bool {
		if n == nil {
			return true
		}
		if n.reading.time < lo {
			return walk(n.right)
		}
		if n.reading.time > hi {
			return walk(n.left)
		}
		return walk(n.left) && fn(n.reading) && walk(n.right)
	}
	return walk(t.root)
}
