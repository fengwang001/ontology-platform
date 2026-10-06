package sla

// 两个隐式键（按坐标排序）的最小树堆，均支持单点增量与前缀和/区间枚举，
// 让裁决路径与天气事件总数、其他订单数量无关（O(1) 读、O(log) 登记）。

// sumTreap 用于天气事件：坐标=区间端点，值=延展量增量，查询原点前缀和。
// 天气区间 [l,r) 登记为 add(l,+e)、add(r,-e)；某承诺时刻 p 的覆盖量
// 即 prefixSum(p)，与天气事件总数无关，仅与树高 O(log W) 相关。
type sumNode struct {
	key   int64
	val   int64
	sum   int64
	prio  uint64
	left  *sumNode
	right *sumNode
}

type sumTreap struct {
	root *sumNode
	seq  uint64
}

func newSumTreap() *sumTreap { return &sumTreap{} }

func (t *sumTreap) add(pos, delta int64) {
	t.seq++
	t.root = sumInsert(t.root, pos, delta, splitmix(t.seq))
}

func (t *sumTreap) prefixSum(pos int64) int64 {
	var total int64
	for n := t.root; n != nil; {
		switch {
		case pos < n.key:
			n = n.left
		case pos == n.key:
			total += sumSub(n.left) + n.val
			return total
		default:
			total += sumSub(n.left) + n.val
			n = n.right
		}
	}
	return total
}

func sumSub(n *sumNode) int64 {
	if n == nil {
		return 0
	}
	return n.sum
}

func sumPull(n *sumNode) {
	n.sum = n.val + sumSub(n.left) + sumSub(n.right)
}

func sumRotateRight(n *sumNode) *sumNode {
	x := n.left
	n.left = x.right
	x.right = n
	sumPull(n)
	sumPull(x)
	return x
}

func sumRotateLeft(n *sumNode) *sumNode {
	x := n.right
	n.right = x.left
	x.left = n
	sumPull(n)
	sumPull(x)
	return x
}

func sumInsert(n *sumNode, key, delta int64, prio uint64) *sumNode {
	if n == nil {
		return &sumNode{key: key, val: delta, sum: delta, prio: prio}
	}
	if key == n.key {
		n.val += delta
		sumPull(n)
		return n
	}
	if key < n.key {
		n.left = sumInsert(n.left, key, delta, prio)
		if n.left.prio < n.prio {
			n = sumRotateRight(n)
		}
	} else {
		n.right = sumInsert(n.right, key, delta, prio)
		if n.right.prio < n.prio {
			n = sumRotateLeft(n)
		}
	}
	sumPull(n)
	return n
}

// orderTreap 用于按原始承诺时刻索引订单，枚举落在某天气区间内的订单。
// 同一坐标（相同原始承诺时刻）可挂多笔订单，故节点持有订单桶。
type orderNode struct {
	key    int64
	orders []*order
	prio   uint64
	left   *orderNode
	right  *orderNode
}

type orderTreap struct {
	root *orderNode
	seq  uint64
}

func newOrderTreap() *orderTreap { return &orderTreap{} }

func (t *orderTreap) insert(o *order) {
	t.seq++
	t.root = orderInsert(t.root, o, splitmix(t.seq^0x9e3779b97f4a7c15))
}

// forEachIn 枚举原始承诺时刻落在 [l,r) 的订单。
func (t *orderTreap) forEachIn(l, r int64, fn func(*order)) {
	var walk func(n *orderNode)
	walk = func(n *orderNode) {
		if n == nil {
			return
		}
		if n.key >= l {
			walk(n.left)
		}
		if n.key >= l && n.key < r {
			for _, o := range n.orders {
				fn(o)
			}
		}
		if n.key < r {
			walk(n.right)
		}
	}
	walk(t.root)
}

func orderRotateRight(n *orderNode) *orderNode {
	x := n.left
	n.left = x.right
	x.right = n
	return x
}

func orderRotateLeft(n *orderNode) *orderNode {
	x := n.right
	n.right = x.left
	x.left = n
	return x
}

func orderInsert(n *orderNode, o *order, prio uint64) *orderNode {
	if n == nil {
		return &orderNode{key: o.promisedAt, orders: []*order{o}, prio: prio}
	}
	if o.promisedAt == n.key {
		n.orders = append(n.orders, o)
		return n
	}
	if o.promisedAt < n.key {
		n.left = orderInsert(n.left, o, prio)
		if n.left.prio < n.prio {
			n = orderRotateRight(n)
		}
	} else {
		n.right = orderInsert(n.right, o, prio)
		if n.right.prio < n.prio {
			n = orderRotateLeft(n)
		}
	}
	return n
}

// splitmix 是确定性伪随机：同一操作序列重放得到相同树形，
// 但裁决结果本身不依赖树的形状。
func splitmix(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}
