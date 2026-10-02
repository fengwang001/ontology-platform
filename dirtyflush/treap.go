package dirtyflush

// treap 是按 (oldest, pageID) 严格升序组织的随机平衡二叉树，
// 用于维护脏页刷写链表。键中加入 pageID 作为次序，保证同一 oldest
// 下也有确定且严格的先后关系。
type tnode struct {
	page   int
	oldest int64
	left   *tnode
	right  *tnode
	prio   uint64
}

type treap struct {
	root *tnode
	rng  uint64
}

func newTreap() *treap {
	return &treap{rng: 0x9E3779B97F4A7C15}
}

// nextPrio 返回树内部确定性的伪随机优先级。
func (t *treap) nextPrio() uint64 {
	x := t.rng
	x ^= x << 13
	x ^= x >> 7
	x ^= x << 17
	t.rng = x
	return x
}

func keyLess(o1 int64, p1 int, o2 int64, p2 int) bool {
	return o1 < o2 || (o1 == o2 && p1 < p2)
}

func rotateRight(n *tnode) *tnode {
	x := n.left
	n.left = x.right
	x.right = n
	return x
}

func rotateLeft(n *tnode) *tnode {
	x := n.right
	n.right = x.left
	x.left = n
	return x
}

// insert 插入一个新节点；调用方须保证该键不存在。
func (t *treap) insert(page int, oldest int64) {
	prio := t.nextPrio()
	t.root = t.insertAt(t.root, page, oldest, prio)
}

func (t *treap) insertAt(n *tnode, page int, oldest int64, prio uint64) *tnode {
	if n == nil {
		return &tnode{page: page, oldest: oldest, prio: prio}
	}
	if keyLess(oldest, page, n.oldest, n.page) {
		n.left = t.insertAt(n.left, page, oldest, prio)
		if n.left.prio > n.prio {
			n = rotateRight(n)
		}
	} else {
		n.right = t.insertAt(n.right, page, oldest, prio)
		if n.right.prio > n.prio {
			n = rotateLeft(n)
		}
	}
	return n
}

// erase 删除键 (oldest,page)；找不到时树不变。
func (t *treap) erase(page int, oldest int64) {
	t.root = eraseAt(t.root, page, oldest)
}

func eraseAt(n *tnode, page int, oldest int64) *tnode {
	if n == nil {
		return nil
	}
	if keyLess(oldest, page, n.oldest, n.page) {
		n.left = eraseAt(n.left, page, oldest)
		return n
	}
	if keyLess(n.oldest, n.page, oldest, page) {
		n.right = eraseAt(n.right, page, oldest)
		return n
	}
	return merge(n.left, n.right)
}

func merge(a, b *tnode) *tnode {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if a.prio > b.prio {
		a.right = merge(a.right, b)
		return a
	}
	b.left = merge(a, b.left)
	return b
}

// first 返回链表首页；空树时 ok 为 false。
func (t *treap) first() (oldest int64, page int, ok bool) {
	n := t.root
	if n == nil {
		return 0, 0, false
	}
	for n.left != nil {
		n = n.left
	}
	return n.oldest, n.page, true
}

// contains 判断键是否存在。
func (t *treap) contains(page int, oldest int64) bool {
	n := t.root
	for n != nil {
		switch {
		case keyLess(oldest, page, n.oldest, n.page):
			n = n.left
		case keyLess(n.oldest, n.page, oldest, page):
			n = n.right
		default:
			return true
		}
	}
	return false
}

// inorder 按键升序把页号追加到 dst。
func (t *treap) inorder(dst []int) []int {
	return appendInorder(t.root, dst)
}

func appendInorder(n *tnode, dst []int) []int {
	if n == nil {
		return dst
	}
	dst = appendInorder(n.left, dst)
	dst = append(dst, n.page)
	dst = appendInorder(n.right, dst)
	return dst
}

// OrderEntry 是链表中一项的 (oldest, pageID) 键。
type OrderEntry struct {
	Oldest int64
	Page   int
}

// entries 返回 (oldest,page) 的升序快照，主要供测试与文档示例使用。
func (t *treap) entries() []OrderEntry {
	var out []OrderEntry
	var walk func(*tnode)
	walk = func(n *tnode) {
		if n == nil {
			return
		}
		walk(n.left)
		out = append(out, OrderEntry{Oldest: n.oldest, Page: n.page})
		walk(n.right)
	}
	walk(t.root)
	return out
}
