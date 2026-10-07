package scheduler

// treap 是以连接级序号为键、以确定性伪随机优先级维持期望平衡的
// 树堆，用于存放待发重新注入段。insert/min/popMin/inorder 的代价
// 均为期望 O(log n)，n 为当前待发重新注入段数。
//
// 优先级由序号的 splitmix64 散列导出，不引入任何随机状态，
// 保证运行结果完全确定。
type treap struct {
	root   *tnode
	n      int
	visits *int64 // 指向调度器的 TreapNodeVisits 计数器，可为 nil
}

type tnode struct {
	s    seg
	pri  uint64
	l, r *tnode
}

func priority(seq int64) uint64 {
	z := uint64(seq) + 0x9e3779b97f4a7c15
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

func (t *treap) len() int { return t.n }

func (t *treap) visit(k int64) {
	if t.visits != nil {
		*t.visits += k
	}
}

// contains 报告序号是否已存在（用于重新注入去重）。
func (t *treap) contains(seq int64) bool {
	cur := t.root
	for cur != nil {
		t.visit(1)
		switch {
		case seq < cur.s.seq:
			cur = cur.l
		case seq > cur.s.seq:
			cur = cur.r
		default:
			return true
		}
	}
	return false
}

// insert 插入新段，调用方须保证序号不存在。
func (t *treap) insert(s seg) {
	t.root = t.insertNode(t.root, &tnode{s: s, pri: priority(s.seq)})
	t.n++
}

func (t *treap) insertNode(root, n *tnode) *tnode {
	t.visit(1)
	if root == nil {
		return n
	}
	if n.s.seq < root.s.seq {
		root.l = t.insertNode(root.l, n)
		if root.l.pri < root.pri {
			return rotateRight(root)
		}
	} else {
		root.r = t.insertNode(root.r, n)
		if root.r.pri < root.pri {
			return rotateLeft(root)
		}
	}
	return root
}

func rotateLeft(root *tnode) *tnode {
	r := root.r
	root.r = r.l
	r.l = root
	return r
}

func rotateRight(root *tnode) *tnode {
	l := root.l
	root.l = l.r
	l.r = root
	return l
}

// min 返回序号最小的段。
func (t *treap) min() (seg, bool) {
	cur := t.root
	if cur == nil {
		return seg{}, false
	}
	for cur.l != nil {
		t.visit(1)
		cur = cur.l
	}
	return cur.s, true
}

// popMin 删除并返回序号最小的段。
func (t *treap) popMin() seg {
	cur := t.root
	var parent *tnode
	for cur.l != nil {
		t.visit(1)
		parent = cur
		cur = cur.l
	}
	out := cur.s
	if parent == nil {
		t.root = cur.r
	} else {
		parent.l = cur.r
	}
	t.n--
	return out
}

// inorder 按序号升序导出全部段。
func (t *treap) inorder() []Seg {
	out := make([]Seg, 0, t.n)
	var walk func(n *tnode)
	walk = func(n *tnode) {
		if n == nil {
			return
		}
		walk(n.l)
		out = append(out, Seg{Seq: n.s.seq, Len: n.s.len, Excluded: n.s.excluded})
		walk(n.r)
	}
	walk(t.root)
	return out
}
