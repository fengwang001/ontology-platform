package leave

// itreap 是一棵按区间起点 from 排序的 Treap，保存同一员工所有未终结请假
// 的闭区间 [from, to]。由于重叠申请会被拒绝，树中区间两两不相交，因此按
// from 排序也就按 to 排序，重叠判定只需前驱/后继查询，期望 O(log n)。
//
// 优先级由假单 ID 确定性哈希得到，不依赖任何随机源，保证相同操作序列
// 重放得到完全相同的树形与结果。
type itreap struct {
	root *tnode
	size int
}

type tnode struct {
	from, to    int
	id          int64
	prio        uint32
	left, right *tnode
}

// prioOf 是确定性的优先级函数（FNV-1a 混合），保证重放一致性。
func prioOf(id int64) uint32 {
	h := uint32(2166136261)
	for i := 0; i < 8; i++ {
		h ^= uint32(byte(uint64(id) >> (8 * i)))
		h *= 16777619
	}
	h ^= h >> 16
	h *= 2246822519
	h ^= h >> 13
	return h
}

func rotateLeft(n *tnode) *tnode {
	r := n.right
	n.right = r.left
	r.left = n
	return r
}

func rotateRight(n *tnode) *tnode {
	l := n.left
	n.left = l.right
	l.right = n
	return l
}

func insertNode(n, x *tnode) *tnode {
	if n == nil {
		return x
	}
	if x.from < n.from {
		n.left = insertNode(n.left, x)
		if n.left.prio < n.prio {
			n = rotateRight(n)
		}
	} else {
		n.right = insertNode(n.right, x)
		if n.right.prio < n.prio {
			n = rotateLeft(n)
		}
	}
	return n
}

func mergeNodes(l, r *tnode) *tnode {
	if l == nil {
		return r
	}
	if r == nil {
		return l
	}
	if l.prio < r.prio {
		l.right = mergeNodes(l.right, r)
		return l
	}
	r.left = mergeNodes(l, r.left)
	return r
}

func removeNode(n *tnode, from int) *tnode {
	if n == nil {
		return nil
	}
	switch {
	case from < n.from:
		n.left = removeNode(n.left, from)
	case from > n.from:
		n.right = removeNode(n.right, from)
	default:
		return mergeNodes(n.left, n.right)
	}
	return n
}

func (t *itreap) insert(from, to int, id int64) {
	t.root = insertNode(t.root, &tnode{from: from, to: to, id: id, prio: prioOf(id)})
	t.size++
}

func (t *itreap) remove(from int) {
	t.root = removeNode(t.root, from)
	t.size--
}

// find 返回起点恰为 from 的节点，用于提前结束时原地更新区间右端点。
func (t *itreap) find(from int) *tnode {
	for n := t.root; n != nil; {
		switch {
		case from < n.from:
			n = n.left
		case from > n.from:
			n = n.right
		default:
			return n
		}
	}
	return nil
}

// pred 返回起点 <= from 的最大区间。
func (t *itreap) pred(from int) *tnode {
	var best *tnode
	for n := t.root; n != nil; {
		if n.from <= from {
			best = n
			n = n.right
		} else {
			n = n.left
		}
	}
	return best
}

// succ 返回起点 > from 的最小区间。
func (t *itreap) succ(from int) *tnode {
	var best *tnode
	for n := t.root; n != nil; {
		if n.from > from {
			best = n
			n = n.left
		} else {
			n = n.right
		}
	}
	return best
}

// overlaps 判定闭区间 [from, to] 是否与树中任一区间有日期重叠。
// 树中区间两两不相交，只需检查 from 的前驱与后继。
func (t *itreap) overlaps(from, to int) bool {
	if p := t.pred(from); p != nil && p.to >= from {
		return true
	}
	if s := t.succ(from); s != nil && s.from <= to {
		return true
	}
	return false
}
