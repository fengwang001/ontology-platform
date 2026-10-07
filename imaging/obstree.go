package imaging

// obstree.go 实现留观位占用的并发数核算结构。
//
// 模型：每段留观 [s,e) 转化为两个事件：s 处 +1，e 处 -1。
// 任意时刻 t 的留观人数 = 所有键 <= t 的事件增量之和（前缀和）。
// 判定新区间 [s,e) 是否可加入，等价于 [s,e) 内最大前缀和 + 1 <= 留观位总数。
//
// 选型：笛卡尔树（键为事件时刻，同时刻事件聚合为一个结点），
// 每个结点维护子树增强信息：
//   sum    —— 子树增量之和
//   maxPre —— 子树中序序列的最大前缀和
//   minKey/maxKey —— 子树键范围，用于区间查询剪枝
// 因此单点增减与区间最大前缀和查询均为 O(log n)，
// 与历史留观总数无关。优先级取键的确定性哈希，保证重放一致。

const negInf = -1 << 60

type obsNode struct {
	key    int
	delta  int
	prio   uint64
	left   *obsNode
	right  *obsNode
	sum    int
	maxPre int
	minKey int
	maxKey int
}

type obsTree struct {
	root   *obsNode
	visits int64
}

func obsSum(n *obsNode) int {
	if n == nil {
		return 0
	}
	return n.sum
}

func obsMaxPre(n *obsNode) int {
	if n == nil {
		return negInf
	}
	return n.maxPre
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func updateO(n *obsNode) {
	ls, rs := obsSum(n.left), obsSum(n.right)
	lm, rm := obsMaxPre(n.left), obsMaxPre(n.right)
	n.sum = ls + n.delta + rs
	n.maxPre = maxInt(lm, maxInt(ls+n.delta, ls+n.delta+rm))
	n.minKey, n.maxKey = n.key, n.key
	if n.left != nil {
		n.minKey = minInt(n.minKey, n.left.minKey)
		n.maxKey = maxInt(n.maxKey, n.left.maxKey)
	}
	if n.right != nil {
		n.minKey = minInt(n.minKey, n.right.minKey)
		n.maxKey = maxInt(n.maxKey, n.right.maxKey)
	}
}

func rotateRightO(n *obsNode) *obsNode {
	l := n.left
	n.left = l.right
	l.right = n
	updateO(n)
	updateO(l)
	return l
}

func rotateLeftO(n *obsNode) *obsNode {
	r := n.right
	n.right = r.left
	r.left = n
	updateO(n)
	updateO(r)
	return r
}

func insertO(n *obsNode, key, delta int, prio uint64, t *obsTree) *obsNode {
	t.visits++
	if n == nil {
		nd := &obsNode{key: key, delta: delta, prio: prio}
		updateO(nd)
		return nd
	}
	switch {
	case key < n.key:
		n.left = insertO(n.left, key, delta, prio, t)
		if n.left.prio < n.prio {
			n = rotateRightO(n)
		}
	case key > n.key:
		n.right = insertO(n.right, key, delta, prio, t)
		if n.right.prio < n.prio {
			n = rotateLeftO(n)
		}
	default:
		n.delta += delta
	}
	updateO(n)
	return n
}

func mergeO(a, b *obsNode, t *obsTree) *obsNode {
	t.visits++
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if a.prio < b.prio {
		a.right = mergeO(a.right, b, t)
		updateO(a)
		return a
	}
	b.left = mergeO(a, b.left, t)
	updateO(b)
	return b
}

func removeO(n *obsNode, key int, t *obsTree) *obsNode {
	t.visits++
	if n == nil {
		return nil
	}
	if key < n.key {
		n.left = removeO(n.left, key, t)
		updateO(n)
		return n
	}
	if key > n.key {
		n.right = removeO(n.right, key, t)
		updateO(n)
		return n
	}
	return mergeO(n.left, n.right, t)
}

// add 在 key 时刻累加增量 delta；增量归零的结点被移除。
func (t *obsTree) add(key, delta int) {
	t.root = insertO(t.root, key, delta, hashKey(key), t)
	if n := t.find(key); n != nil && n.delta == 0 {
		t.root = removeO(t.root, key, t)
	}
}

func (t *obsTree) find(key int) *obsNode {
	for n := t.root; n != nil; {
		t.visits++
		if key < n.key {
			n = n.left
		} else if key > n.key {
			n = n.right
		} else {
			return n
		}
	}
	return nil
}

// prefixSumBefore 返回键严格小于 key 的增量之和。
func (t *obsTree) prefixSumBefore(key int) int {
	acc := 0
	for n := t.root; n != nil; {
		t.visits++
		if n.key < key {
			acc += obsSum(n.left) + n.delta
			n = n.right
		} else {
			n = n.left
		}
	}
	return acc
}

// queryRange 返回键落在 [lo,hi) 内子序列的 (增量和, 最大前缀和)。
// 空区间返回 (0, negInf)。
func (t *obsTree) queryRange(lo, hi int) (int, int) {
	return queryO(t.root, lo, hi, t)
}

func queryO(n *obsNode, lo, hi int, t *obsTree) (int, int) {
	if n == nil {
		return 0, negInf
	}
	t.visits++
	if n.maxKey < lo || n.minKey >= hi {
		return 0, negInf
	}
	if lo <= n.minKey && n.maxKey < hi {
		return n.sum, n.maxPre
	}
	ls, lm := queryO(n.left, lo, hi, t)
	rs, rm := queryO(n.right, lo, hi, t)
	if lo <= n.key && n.key < hi {
		return ls + n.delta + rs, maxInt(lm, maxInt(ls+n.delta, ls+n.delta+rm))
	}
	return ls + rs, maxInt(lm, ls+rm)
}

// maxOccupancy 返回 [s,e) 内的最大留观人数（不含待加入的新区间）。
func (t *obsTree) maxOccupancy(s, e int) int {
	base := t.prefixSumBefore(s)
	_, mp := t.queryRange(s, e)
	// 时刻 t 的留观人数 = 键 <= t 的增量之和。
	// 若 s 处无事件，则 t=s 处人数为 base（空前缀 0 可达）；
	// 若 s 处有事件，则 [s,e) 内每个时刻的人数必为 base 加上
	// 区间内某非空前缀，0 前缀不可达。
	p := mp
	if t.find(s) == nil && p < 0 {
		p = 0
	}
	return base + p
}

// canAdd 报告加入新区间 [s,e) 后留观人数是否仍不超过 cap。
func (t *obsTree) canAdd(s, e, cap int) bool {
	return t.maxOccupancy(s, e)+1 <= cap
}
