package imaging

// treap.go 实现按开始时刻索引的占用区间集合。
//
// 选型：笛卡尔树（treap），键为区间起点，优先级取键的确定性哈希
// （splitmix64），因此结构形态只取决于键集合，与插入顺序无关，
// 相同操作序列重放得到完全相同的内部结构。
//
// 由于同一设备的占用区间两两不重叠，区间起点唯一，可作为键。
// 重叠判定只需考察 [s,e) 的前驱与后继区间，代价 O(log n)，
// 与设备历史预约总数无关（仅随树高对数增长）。

// hashKey 将整数键确定性地打散为 treap 优先级。
func hashKey(k int) uint64 {
	x := uint64(k) + 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}

type intervalNode struct {
	start, end int
	bookingID  string
	prio       uint64
	left       *intervalNode
	right      *intervalNode
}

// intervalSet 是单设备占用区间的有序集合。
// visits 统计访问过的结点数，用于性能可验证性（见 perf_test.go）。
type intervalSet struct {
	root   *intervalNode
	size   int
	visits int64
}

// predecessor 返回起点严格小于 s 的最大起点区间。
func (t *intervalSet) predecessor(s int) *intervalNode {
	var best *intervalNode
	for n := t.root; n != nil; {
		t.visits++
		if n.start < s {
			best = n
			n = n.right
		} else {
			n = n.left
		}
	}
	return best
}

// lowerBound 返回起点大于等于 s 的最小起点区间。
func (t *intervalSet) lowerBound(s int) *intervalNode {
	var best *intervalNode
	for n := t.root; n != nil; {
		t.visits++
		if n.start >= s {
			best = n
			n = n.left
		} else {
			n = n.right
		}
	}
	return best
}

// overlaps 报告 [s,e) 是否与集合中任一区间重叠（区间均左闭右开）。
func (t *intervalSet) overlaps(s, e int) bool {
	if p := t.predecessor(s); p != nil && p.end > s {
		return true
	}
	if n := t.lowerBound(s); n != nil && n.start < e {
		return true
	}
	return false
}

func rotateRightI(n *intervalNode) *intervalNode {
	l := n.left
	n.left = l.right
	l.right = n
	return l
}

func rotateLeftI(n *intervalNode) *intervalNode {
	r := n.right
	n.right = r.left
	r.left = n
	return r
}

func insertI(n *intervalNode, start, end int, id string, prio uint64, t *intervalSet) *intervalNode {
	t.visits++
	if n == nil {
		return &intervalNode{start: start, end: end, bookingID: id, prio: prio}
	}
	if start < n.start {
		n.left = insertI(n.left, start, end, id, prio, t)
		if n.left.prio < n.prio {
			n = rotateRightI(n)
		}
	} else {
		n.right = insertI(n.right, start, end, id, prio, t)
		if n.right.prio < n.prio {
			n = rotateLeftI(n)
		}
	}
	return n
}

// insert 插入区间 [start,end)。调用方保证起点不与既有区间重复。
func (t *intervalSet) insert(start, end int, id string) {
	t.root = insertI(t.root, start, end, id, hashKey(start), t)
	t.size++
}

func mergeI(a, b *intervalNode, t *intervalSet) *intervalNode {
	t.visits++
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if a.prio < b.prio {
		a.right = mergeI(a.right, b, t)
		return a
	}
	b.left = mergeI(a, b.left, t)
	return b
}

func removeI(n *intervalNode, start int, t *intervalSet) *intervalNode {
	t.visits++
	if n == nil {
		return nil
	}
	if start < n.start {
		n.left = removeI(n.left, start, t)
		return n
	}
	if start > n.start {
		n.right = removeI(n.right, start, t)
		return n
	}
	return mergeI(n.left, n.right, t)
}

// remove 删除起点为 start 的区间。
func (t *intervalSet) remove(start int) {
	t.root = removeI(t.root, start, t)
	t.size--
}

// forEach 按起点升序遍历全部区间（仅测试与不变量检查使用）。
func (t *intervalSet) forEach(fn func(start, end int, id string)) {
	var walk func(n *intervalNode)
	walk = func(n *intervalNode) {
		if n == nil {
			return
		}
		walk(n.left)
		fn(n.start, n.end, n.bookingID)
		walk(n.right)
	}
	walk(t.root)
}
