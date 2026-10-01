package interval

// less 比较键 (lo, id)，先 lo 后 id，保证同 lo 下按编号升序。
func less(lo1, id1, lo2, id2 int64) bool {
	if lo1 != lo2 {
		return lo1 < lo2
	}
	return id1 < id2
}

func heightOf(n *node) int {
	if n == nil {
		return 0
	}
	return n.height
}

func maxHiOf(n *node) int64 {
	if n == nil {
		return 0
	}
	return n.maxHi
}

// recalc 由子节点重算 height 与 maxHi，删除后沿回溯路径调用，
// 保证增强信息始终精确而非惰性残留。
func (n *node) recalc() {
	n.height = 1 + max(heightOf(n.left), heightOf(n.right))
	n.maxHi = max(n.hi, max(maxHiOf(n.left), maxHiOf(n.right)))
}

func (n *node) balanceFactor() int {
	return heightOf(n.left) - heightOf(n.right)
}

func rotateRight(y *node) *node {
	x := y.left
	y.left = x.right
	x.right = y
	y.recalc()
	x.recalc()
	return x
}

func rotateLeft(x *node) *node {
	y := x.right
	x.right = y.left
	y.left = x
	x.recalc()
	y.recalc()
	return y
}

// rebalance 恢复 AVL 不变量；旋转确定性取决于键与插入顺序，
// 相同操作序列重放得到完全相同的树形。
func rebalance(n *node) *node {
	n.recalc()
	switch bf := n.balanceFactor(); {
	case bf > 1:
		if n.left.balanceFactor() < 0 {
			n.left = rotateLeft(n.left)
		}
		return rotateRight(n)
	case bf < -1:
		if n.right.balanceFactor() > 0 {
			n.right = rotateRight(n.right)
		}
		return rotateLeft(n)
	}
	return n
}

// insert 按 (lo, id) 插入；调用方需保证键不存在。
func insert(n *node, lo, hi, id int64) *node {
	if n == nil {
		return &node{lo: lo, hi: hi, id: id, maxHi: hi, height: 1}
	}
	if less(lo, id, n.lo, n.id) {
		n.left = insert(n.left, lo, hi, id)
	} else {
		n.right = insert(n.right, lo, hi, id)
	}
	return rebalance(n)
}

// removeMin 删除子树最小键节点，返回新子树根与被摘除的节点。
func removeMin(n *node) (*node, *node) {
	if n.left == nil {
		return n.right, n
	}
	var m *node
	n.left, m = removeMin(n.left)
	return rebalance(n), m
}

// remove 按键 (lo, id) 删除；调用方需保证键存在。
func remove(n *node, lo, id int64) *node {
	switch {
	case less(lo, id, n.lo, n.id):
		n.left = remove(n.left, lo, id)
	case less(n.lo, n.id, lo, id):
		n.right = remove(n.right, lo, id)
	default:
		if n.left == nil {
			return n.right
		}
		if n.right == nil {
			return n.left
		}
		var succ *node
		n.right, succ = removeMin(n.right)
		n.lo, n.hi, n.id = succ.lo, succ.hi, succ.id
	}
	return rebalance(n)
}
