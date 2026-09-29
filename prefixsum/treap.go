package prefixsum

// node 是带优先级的 Treap 节点；sub 为整棵子树的值之和。
type node struct {
	key              int64
	val              int64
	sub              int64
	minPref, maxPref int64
	sz               int
	prio             uint64
	left             *node
	right            *node
}

func subOf(n *node) int64 {
	if n == nil {
		return 0
	}
	return n.sub
}

func sizeOf(n *node) int {
	if n == nil {
		return 0
	}
	return n.sz
}

// addChecked 执行 int64 加法，第二返回值为 false 表示溢出。
func addChecked(a, b int64) (int64, bool) {
	if b > 0 && a > (1<<63-1)-b {
		return 0, false
	}
	if b < 0 && a < (-1<<63)-b {
		return 0, false
	}
	return a + b, true
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// buildNode 以给定子节点构造节点并重算 sz 与 sub；溢出时失败。
// 所有更新都经由此函数，保证任何被接受状态的子树和与逐键前缀和
// （子树内、从首个键起算）均在 int64 范围内。仅子树总和合法并不足以
// 保证每个前缀和合法（例如 [MaxInt64, MaxInt64, MinInt64]），因此额外
// 维护子树内前缀和（含空前缀 0）的最小值与最大值。
func buildNode(key, val int64, prio uint64, left, right *node) (*node, error) {
	sum, ok := addChecked(subOf(left), val)
	if !ok {
		return nil, errOverflow("prefix sum overflow while rebuilding subtree")
	}
	sum, ok = addChecked(sum, subOf(right))
	if !ok {
		return nil, errOverflow("prefix sum overflow while rebuilding subtree")
	}

	// 序列为 left 全部键、当前键、right 全部键。
	// 到当前键为止的前缀和 = left.sub + val。
	mid, ok := addChecked(subOf(left), val)
	if !ok {
		return nil, errOverflow("prefix sum overflow while rebuilding subtree")
	}
	minPref := minInt64(prefOf(left, true), mid)
	maxPref := maxInt64(prefOf(left, false), mid)

	// right 的前缀整体平移 mid；其 p_0=0 平移后即 mid，已计入。
	rMin, ok := addChecked(mid, prefOf(right, true))
	if !ok {
		return nil, errOverflow("prefix sum overflow while rebuilding subtree")
	}
	rMax, ok := addChecked(mid, prefOf(right, false))
	if !ok {
		return nil, errOverflow("prefix sum overflow while rebuilding subtree")
	}
	minPref = minInt64(minPref, rMin)
	maxPref = maxInt64(maxPref, rMax)

	return &node{
		key:     key,
		val:     val,
		sub:     sum,
		minPref: minPref,
		maxPref: maxPref,
		sz:      sizeOf(left) + 1 + sizeOf(right),
		prio:    prio,
		left:    left,
		right:   right,
	}, nil
}

// prefOf 返回子树内前缀和（含空前缀 0）的极值；min 为 true 取最小值。
func prefOf(n *node, min bool) int64 {
	if n == nil {
		return 0
	}
	if min {
		return n.minPref
	}
	return n.maxPref
}

// insertOrReplace 插入或替换，返回新根；existed 表示键原本存在。
// 函数式更新：仅重建搜索路径与旋转涉及的节点，失败时旧树保持原样。
func (v *View) insertOrReplace(n *node, key, val int64) (root *node, existed bool, err error) {
	if n == nil {
		nn, err := buildNode(key, val, v.newPrio(), nil, nil)
		return nn, false, err
	}
	switch {
	case key == n.key:
		nn, err := buildNode(key, val, n.prio, n.left, n.right)
		return nn, true, err
	case key < n.key:
		nl, existed, err := v.insertOrReplace(n.left, key, val)
		if err != nil {
			return nil, false, err
		}
		if nl.prio < n.prio {
			parent, err := buildNode(n.key, n.val, n.prio, nl.right, n.right)
			if err != nil {
				return nil, false, err
			}
			root, err := buildNode(nl.key, nl.val, nl.prio, nl.left, parent)
			return root, existed, err
		}
		root, err := buildNode(n.key, n.val, n.prio, nl, n.right)
		return root, existed, err
	default:
		nr, existed, err := v.insertOrReplace(n.right, key, val)
		if err != nil {
			return nil, false, err
		}
		if nr.prio < n.prio {
			parent, err := buildNode(n.key, n.val, n.prio, n.left, nr.left)
			if err != nil {
				return nil, false, err
			}
			root, err := buildNode(nr.key, nr.val, nr.prio, parent, nr.right)
			return root, existed, err
		}
		root, err := buildNode(n.key, n.val, n.prio, n.left, nr)
		return root, existed, err
	}
}

// erase 删除键，返回新根与是否删除成功。
func (v *View) erase(n *node, key int64) (root *node, removed bool, err error) {
	if n == nil {
		return nil, false, nil
	}
	switch {
	case key < n.key:
		nl, removed, err := v.erase(n.left, key)
		if err != nil {
			return nil, false, err
		}
		root, err := buildNode(n.key, n.val, n.prio, nl, n.right)
		return root, removed, err
	case key > n.key:
		nr, removed, err := v.erase(n.right, key)
		if err != nil {
			return nil, false, err
		}
		root, err := buildNode(n.key, n.val, n.prio, n.left, nr)
		return root, removed, err
	default:
		merged, err := v.merge(n.left, n.right)
		return merged, true, err
	}
}

// merge 合并两棵键域不相交的 Treap（left 全部键小于 right）。
func (v *View) merge(left, right *node) (*node, error) {
	if left == nil {
		return right, nil
	}
	if right == nil {
		return left, nil
	}
	if left.prio < right.prio {
		mr, err := v.merge(left.right, right)
		if err != nil {
			return nil, err
		}
		return buildNode(left.key, left.val, left.prio, left.left, mr)
	}
	ml, err := v.merge(left, right.left)
	if err != nil {
		return nil, err
	}
	return buildNode(right.key, right.val, right.prio, ml, right.right)
}

// prefixSum 返回所有不大于 key 的存在键值之和。
func prefixSum(n *node, key int64) int64 {
	if n == nil {
		return 0
	}
	switch {
	case key < n.key:
		return prefixSum(n.left, key)
	case key == n.key:
		return subOf(n.left) + n.val
	default:
		return subOf(n.left) + n.val + prefixSum(n.right, key)
	}
}

// find 返回键对应节点，不存在返回 nil。
func find(n *node, key int64) *node {
	for n != nil {
		switch {
		case key < n.key:
			n = n.left
		case key > n.key:
			n = n.right
		default:
			return n
		}
	}
	return nil
}

// flatten 按键升序追加节点到 dst。
func flatten(n *node, dst []Entry) []Entry {
	if n == nil {
		return dst
	}
	dst = flatten(n.left, dst)
	var sum int64
	if len(dst) > 0 {
		sum = dst[len(dst)-1].PrefixSum
	}
	dst = append(dst, Entry{Key: n.key, Value: n.val, PrefixSum: sum + n.val})
	dst = flatten(n.right, dst)
	return dst
}

// countGE 返回子树中键 >= key 的节点数。
func countGE(n *node, key int64) int {
	if n == nil {
		return 0
	}
	switch {
	case key < n.key:
		return countGE(n.left, key) + 1 + sizeOf(n.right)
	case key == n.key:
		return 1 + sizeOf(n.right)
	default:
		return countGE(n.right, key)
	}
}
