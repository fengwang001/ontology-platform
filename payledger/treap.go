package payledger

// sumNode 是按天组织的笛卡尔树（treap）节点，维护子树金额和，
// 用于已过期持有桶的任意历史时刻区间求和。
// 优先级由 day 确定性散列得到；即使发生哈希碰撞，结构仍是合法 BST，
// 只影响平衡性，不影响任何结果的正确性。
type sumNode struct {
	day         int64
	amt         int64 // 该天桶的金额
	sum         int64 // 子树金额和
	prio        uint64
	left, right *sumNode
}

func splitmix64(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}

func nodeSum(n *sumNode) int64 {
	if n == nil {
		return 0
	}
	return n.sum
}

func (n *sumNode) recalc() {
	n.sum = n.amt + nodeSum(n.left) + nodeSum(n.right)
}

func rotateRight(n *sumNode) *sumNode {
	l := n.left
	n.left = l.right
	l.right = n
	n.recalc()
	l.recalc()
	return l
}

func rotateLeft(n *sumNode) *sumNode {
	r := n.right
	n.right = r.left
	r.left = n
	n.recalc()
	r.recalc()
	return r
}

// upsert 给 day 桶加 delta（可为负）；桶归零时删除节点。
func upsert(n *sumNode, day, delta int64) *sumNode {
	if n == nil {
		return &sumNode{day: day, amt: delta, sum: delta, prio: splitmix64(uint64(day))}
	}
	switch {
	case day == n.day:
		n.amt += delta
		if n.amt == 0 {
			return mergeNodes(n.left, n.right)
		}
		n.recalc()
		return n
	case day < n.day:
		n.left = upsert(n.left, day, delta)
		if n.left != nil && n.left.prio < n.prio {
			return rotateRight(n)
		}
		n.recalc()
		return n
	default:
		n.right = upsert(n.right, day, delta)
		if n.right != nil && n.right.prio < n.prio {
			return rotateLeft(n)
		}
		n.recalc()
		return n
	}
}

func mergeNodes(a, b *sumNode) *sumNode {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if a.prio < b.prio {
		a.right = mergeNodes(a.right, b)
		a.recalc()
		return a
	}
	b.left = mergeNodes(a, b.left)
	b.recalc()
	return b
}

// sumLess 返回所有 day < t 的桶金额之和。O(log n) 期望。
func sumLess(n *sumNode, t int64) int64 {
	if n == nil {
		return 0
	}
	if n.day < t {
		return nodeSum(n.left) + n.amt + sumLess(n.right, t)
	}
	return sumLess(n.left, t)
}
