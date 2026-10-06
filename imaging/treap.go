package imaging

import "hash/fnv"

func priority64(key uint64) uint64 {
	h := fnv.New64a()
	var buf [8]byte
	for i := 0; i < 8; i++ {
		buf[i] = byte(key >> (8 * i))
	}
	_, _ = h.Write(buf[:])
	return h.Sum64()
}

// ---- 键多重集合（确定性 treap）：插入/删除 O(log n)，countLE(x) O(log n) ----

type msNode struct {
	key         int
	count       int
	size        int
	prio        uint64
	left, right *msNode
}

func (n *msNode) pull() {
	n.size = n.count
	if n.left != nil {
		n.size += n.left.size
	}
	if n.right != nil {
		n.size += n.right.size
	}
}

type multiSet struct{ root *msNode }

func msRotateRight(n *msNode) *msNode {
	x := n.left
	n.left = x.right
	x.right = n
	n.pull()
	x.pull()
	return x
}

func msRotateLeft(n *msNode) *msNode {
	x := n.right
	n.right = x.left
	x.left = n
	n.pull()
	x.pull()
	return x
}

func msInsert(n *msNode, key int, salt int64) *msNode {
	if n == nil {
		return &msNode{key: key, count: 1, size: 1, prio: priority64(uint64(key) ^ uint64(salt)*0x9E3779B97F4A7C15)}
	}
	switch {
	case key < n.key:
		n.left = msInsert(n.left, key, salt)
		if n.left.prio > n.prio {
			n = msRotateRight(n)
		}
	case key > n.key:
		n.right = msInsert(n.right, key, salt)
		if n.right.prio > n.prio {
			n = msRotateLeft(n)
		}
	default:
		n.count++
	}
	n.pull()
	return n
}

func msErase(n *msNode, key int) *msNode {
	if n == nil {
		return nil
	}
	switch {
	case key < n.key:
		n.left = msErase(n.left, key)
	case key > n.key:
		n.right = msErase(n.right, key)
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
				n = msRotateRight(n)
				n.right = msErase(n.right, key)
			} else {
				n = msRotateLeft(n)
				n.left = msErase(n.left, key)
			}
		}
	}
	n.pull()
	return n
}

func (m *multiSet) insert(key int, salt int64) { m.root = msInsert(m.root, key, salt) }
func (m *multiSet) erase(key int)              { m.root = msErase(m.root, key) }

// countLE 返回键 <= x 的元素个数，O(log n)。
func (m *multiSet) countLE(x int) int {
	total := 0
	for n := m.root; n != nil; {
		switch {
		case x < n.key:
			n = n.left
		case x > n.key:
			total += n.count
			if n.left != nil {
				total += n.left.size
			}
			n = n.right
		default:
			total += n.count
			if n.left != nil {
				total += n.left.size
			}
			return total
		}
	}
	return total
}

func msInorder(n *msNode, out *[]int) {
	if n == nil {
		return
	}
	msInorder(n.left, out)
	for range n.count {
		*out = append(*out, n.key)
	}
	msInorder(n.right, out)
}

func (m *multiSet) keys() []int {
	var out []int
	msInorder(m.root, &out)
	return out
}

// rangeKeys 返回升序的、满足 lo <= key <= hi 的全部键（含重复）。
// 复杂度 O(log n + k)，k 为窗口内事件数，与窗口外历史无关。
func (m *multiSet) rangeKeys(lo, hi int) []int {
	var out []int
	msInRange(m.root, lo, hi, &out)
	return out
}

func msInRange(n *msNode, lo, hi int, out *[]int) {
	if n == nil {
		return
	}
	if n.key < lo {
		msInRange(n.right, lo, hi, out)
		return
	}
	if n.key > hi {
		msInRange(n.left, lo, hi, out)
		return
	}
	msInRange(n.left, lo, hi, out)
	for range n.count {
		*out = append(*out, n.key)
	}
	msInRange(n.right, lo, hi, out)
}

// ---- 区间树（确定性 treap，按 (start,id) 排序，子树维护 maxEnd）----
// overlapsAny: O(log n + k)；insert/remove: O(log n)。与历史总数无关。

type intervalNode struct {
	keyStart    int
	id          uint64
	end         int
	maxEnd      int
	prio        uint64
	left, right *intervalNode
}

type intervalTree struct{ root *intervalNode }

func (n *intervalNode) pullMax() {
	n.maxEnd = n.end
	if n.left != nil && n.left.maxEnd > n.maxEnd {
		n.maxEnd = n.left.maxEnd
	}
	if n.right != nil && n.right.maxEnd > n.maxEnd {
		n.maxEnd = n.right.maxEnd
	}
}

func lessKey(ks, id uint64, ks2, id2 uint64) bool {
	return ks < ks2 || (ks == ks2 && id < id2)
}

func itRotateRight(n *intervalNode) *intervalNode {
	x := n.left
	n.left = x.right
	x.right = n
	n.pullMax()
	x.pullMax()
	return x
}

func itRotateLeft(n *intervalNode) *intervalNode {
	x := n.right
	n.right = x.left
	x.left = n
	n.pullMax()
	x.pullMax()
	return x
}

func itInsert(n, node *intervalNode) *intervalNode {
	if n == nil {
		node.maxEnd = node.end
		return node
	}
	if lessKey(uint64(node.keyStart), node.id, uint64(n.keyStart), n.id) {
		n.left = itInsert(n.left, node)
		if n.left.prio > n.prio {
			n = itRotateRight(n)
		}
	} else {
		n.right = itInsert(n.right, node)
		if n.right.prio > n.prio {
			n = itRotateLeft(n)
		}
	}
	n.pullMax()
	return n
}

func itDelete(n *intervalNode, start int, id uint64) *intervalNode {
	if n == nil {
		return nil
	}
	if lessKey(uint64(start), id, uint64(n.keyStart), n.id) {
		n.left = itDelete(n.left, start, id)
	} else if lessKey(uint64(n.keyStart), n.id, uint64(start), id) {
		n.right = itDelete(n.right, start, id)
	} else {
		switch {
		case n.left == nil:
			return n.right
		case n.right == nil:
			return n.left
		default:
			if n.left.prio > n.right.prio {
				n = itRotateRight(n)
				n.right = itDelete(n.right, start, id)
			} else {
				n = itRotateLeft(n)
				n.left = itDelete(n.left, start, id)
			}
		}
	}
	n.pullMax()
	return n
}

func (t *intervalTree) insert(id uint64, start, end int) {
	t.root = itInsert(t.root, &intervalNode{keyStart: start, id: id, end: end, maxEnd: end, prio: priority64(id)})
}

func (t *intervalTree) remove(id uint64, start, end int) {
	t.root = itDelete(t.root, start, id)
}

// overlapsAny 判断半开区间 [start,end) 是否与除 skipID 外的任一区间重叠；恰相接不算。
func (t *intervalTree) overlapsAny(start, end int, skipID uint64) bool {
	return itOverlaps(t.root, start, end, skipID)
}

func itOverlaps(n *intervalNode, start, end int, skipID uint64) bool {
	if n == nil || n.maxEnd <= start {
		return false
	}
	// 左子树可能含有 start 更小、end 更大的区间，先查。
	if itOverlaps(n.left, start, end, skipID) {
		return true
	}
	if n.id != skipID && n.keyStart < end && start < n.end {
		return true
	}
	// 右子树节点 start 均不小于当前节点 start；已越过查询末端则不可能重叠。
	if n.keyStart >= end {
		return false
	}
	return itOverlaps(n.right, start, end, skipID)
}

func itCollect(n *intervalNode, out *[]Interval) {
	if n == nil {
		return
	}
	itCollect(n.left, out)
	*out = append(*out, Interval{Start: n.keyStart, End: n.end})
	itCollect(n.right, out)
}

func (t *intervalTree) intervals() []Interval {
	var out []Interval
	itCollect(t.root, &out)
	return out
}
