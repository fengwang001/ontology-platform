package nseccache

// entry 是一条存活的 NSEC 记录及其到期时刻。
type entry struct {
	owner canonName
	next  canonName
	types map[uint16]bool
	ttl   int
	exp   int

	rec Record // 原始记录（Owner 为原始大小写文本），供结果返回

	// AVL 节点信息
}

// tree 是以 owner 规范序为键的 AVL 树。
// cmp 非 nil 时，每发生一次名字规范序比较就向 *cmp 加一。
type tree struct {
	root    *node
	wrap    *node // 唯一回绕记录（owner >= next）；多于一条时为 nil
	wrapCnt int
	cmp     *int64
}

func (t *tree) compare(a, b canonName) int {
	if t.cmp != nil {
		*t.cmp++
	}
	return compareCanon(a, b)
}

func (t *tree) isWrap(e *entry) bool {
	return t.compare(e.owner, e.next) >= 0
}

// insert 加入节点；owner 已存在时替换其 entry 并返回旧 entry。
func (t *tree) insert(e *entry) *entry {
	if t.root == nil {
		n := &node{entry: e, h: 1}
		t.root = n
		t.addWrap(n)
		return nil
	}
	cur := t.root
	for {
		c := t.compare(e.owner, cur.entry.owner)
		if c == 0 {
			old := cur.entry
			cur.entry = e
			if old != nil {
				t.removeWrap(old)
			}
			t.addWrap(cur)
			return old
		}
		if c < 0 {
			if cur.left == nil {
				n := &node{entry: e, parent: cur, h: 1}
				cur.left = n
				t.afterAdd(n)
				t.addWrap(n)
				return nil
			}
			cur = cur.left
		} else {
			if cur.right == nil {
				n := &node{entry: e, parent: cur, h: 1}
				cur.right = n
				t.afterAdd(n)
				t.addWrap(n)
				return nil
			}
			cur = cur.right
		}
	}
}

// delete 删除指定节点。
func (t *tree) delete(n *node) *entry {
	switch {
	case n.left != nil && n.right != nil:
		suc := n.right
		for suc.left != nil {
			suc = suc.left
		}
		n.entry = suc.entry
		n = suc
		removed := suc.entry
		t.unlink(suc)
		t.removeWrap(removed)
		return removed
	default:
		removed := n.entry
		t.unlink(n)
		t.removeWrap(removed)
		return removed
	}
}

// unlink 摘除至多一个孩子的节点并自平衡。
func (t *tree) unlink(n *node) {
	var child *node
	if n.left != nil {
		child = n.left
	} else {
		child = n.right
	}
	rebalanceFrom := n.parent
	if child != nil {
		child.parent = n.parent
	}
	if n.parent == nil {
		t.root = child
	} else if n == n.parent.left {
		n.parent.left = child
	} else {
		n.parent.right = child
	}
	t.afterDelete(rebalanceFrom)
}

// find 返回 owner 等于 key 的节点。
func (t *tree) find(key canonName) *node {
	cur := t.root
	for cur != nil {
		c := t.compare(key, cur.entry.owner)
		if c == 0 {
			return cur
		}
		if c < 0 {
			cur = cur.left
		} else {
			cur = cur.right
		}
	}
	return nil
}

// predecessor 返回 owner 严格小于 key 的最大节点。
func (t *tree) predecessor(key canonName) *node {
	var best *node
	cur := t.root
	for cur != nil {
		c := t.compare(key, cur.entry.owner)
		if c == 0 {
			return inorderPredecessor(cur)
		}
		if c > 0 {
			best = cur
			cur = cur.right
		} else {
			cur = cur.left
		}
	}
	return best
}

func inorderPredecessor(n *node) *node {
	if n.left != nil {
		cur := n.left
		for cur.right != nil {
			cur = cur.right
		}
		return cur
	}
	cur := n
	for cur.parent != nil && cur == cur.parent.left {
		cur = cur.parent
	}
	return cur.parent
}

// rightmost 返回 owner 最大的节点。
func (t *tree) rightmost() *node {
	cur := t.root
	for cur != nil && cur.right != nil {
		cur = cur.right
	}
	return cur
}

// findCover 返回覆盖 x 的存活记录中 owner 最大者；无则 nil。
// 保证数据集（区间两两不重叠、回绕至多一条）下为 O(log n) 次比较；
// 若存在多条回绕记录则退化为线性扫描（仅要求结果正确）。
func (t *tree) findCover(x canonName) *entry {
	if t.root == nil {
		return nil
	}
	if t.wrapCnt <= 1 {
		pred := t.predecessor(x)
		var cand *node
		switch {
		case pred != nil && !t.isWrap(pred.entry) &&
			t.compare(x, pred.entry.next) < 0:
			cand = pred
		case t.wrap != nil && t.compare(t.wrap.entry.owner, x) < 0:
			cand = t.wrap
		}
		if cand != nil {
			return cand.entry
		}
		if t.wrap != nil && t.compare(x, t.wrap.entry.next) < 0 {
			return t.wrap.entry
		}
		return nil
	}
	return t.scanCover(x)
}

func (t *tree) scanCover(x canonName) *entry {
	var best *entry
	var walk func(n *node)
	walk = func(n *node) {
		if n == nil {
			return
		}
		walk(n.left)
		e := n.entry
		covers := false
		if t.compare(e.owner, e.next) < 0 {
			covers = t.compare(e.owner, x) < 0 && t.compare(x, e.next) < 0
		} else {
			covers = t.compare(x, e.owner) > 0 || t.compare(x, e.next) < 0
		}
		if covers && (best == nil || t.compare(e.owner, best.owner) > 0) {
			best = e
		}
		walk(n.right)
	}
	walk(t.root)
	return best
}

func (t *tree) len() int {
	return countNodes(t.root)
}

func countNodes(n *node) int {
	if n == nil {
		return 0
	}
	return 1 + countNodes(n.left) + countNodes(n.right)
}

// ---- 回绕记录维护 ----

func (t *tree) addWrap(n *node) {
	if compareCanon(n.entry.owner, n.entry.next) < 0 {
		return
	}
	t.wrapCnt++
	if t.wrapCnt == 1 {
		t.wrap = n
	} else {
		t.wrap = nil
	}
}

func (t *tree) removeWrap(e *entry) {
	if compareCanon(e.owner, e.next) >= 0 {
		t.wrapCnt--
		if t.wrapCnt == 1 {
			t.wrap = t.scanWrap()
		} else if t.wrapCnt == 0 {
			t.wrap = nil
		}
	}
}

func (t *tree) scanWrap() *node {
	var found *node
	var walk func(n *node)
	walk = func(n *node) {
		if n == nil {
			return
		}
		walk(n.left)
		if compareCanon(n.entry.owner, n.entry.next) >= 0 {
			found = n
		}
		walk(n.right)
	}
	walk(t.root)
	return found
}

// ---- AVL 平衡 ----

func height(n *node) int {
	if n == nil {
		return 0
	}
	return n.h
}

func updateHeight(n *node) {
	h := height(n.left)
	if height(n.right) > h {
		h = height(n.right)
	}
	n.h = h + 1
}

func (t *tree) rotateRight(y *node) *node {
	x := y.left
	y.left = x.right
	if x.right != nil {
		x.right.parent = y
	}
	x.parent = y.parent
	if y.parent == nil {
		t.root = x
	} else if y == y.parent.left {
		y.parent.left = x
	} else {
		y.parent.right = x
	}
	x.right = y
	y.parent = x
	updateHeight(y)
	updateHeight(x)
	return x
}

func (t *tree) rotateLeft(x *node) *node {
	y := x.right
	x.right = y.left
	if y.left != nil {
		y.left.parent = x
	}
	y.parent = x.parent
	if x.parent == nil {
		t.root = y
	} else if x == x.parent.left {
		x.parent.left = y
	} else {
		x.parent.right = y
	}
	y.left = x
	x.parent = y
	updateHeight(x)
	updateHeight(y)
	return y
}

func (t *tree) rebalance(n *node) {
	for n != nil {
		updateHeight(n)
		bal := height(n.left) - height(n.right)
		switch {
		case bal > 1:
			if height(n.left.left) < height(n.left.right) {
				t.rotateLeft(n.left)
			}
			n = t.rotateRight(n)
		case bal < -1:
			if height(n.right.right) < height(n.right.left) {
				t.rotateRight(n.right)
			}
			n = t.rotateLeft(n)
		}
		n = n.parent
	}
}

func (t *tree) afterAdd(n *node) {
	t.rebalance(n)
}

func (t *tree) afterDelete(n *node) {
	t.rebalance(n)
}

type node struct {
	entry  *entry
	left   *node
	right  *node
	parent *node
	h      int
}
