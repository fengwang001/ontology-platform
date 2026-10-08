package bitemporal

// node 是持久化（不可变）treap 的一个节点。
//
// 每个业务时间起点是一个节点；同一业务起点的多次写入形成按系统时间升序的
// 覆盖链。树按 bizStart 有序，优先级由 bizStart 的确定性哈希给出，因此：
//   - 结构只取决于“存在哪些起点”，与插入顺序无关，重放结果逐字节一致；
//   - 所有更新走路径拷贝，旧快照永久可读，天然保存每个系统时刻的索引状态。
type node struct {
	key      int64  // 业务时间起点
	priority uint64 // splitmix64(key)
	left     *node
	right    *node
	chain    []int64 // 该起点各次提交的系统版本号（升序）
}

// insert 返回插入提交后的新根；不修改原树。
//
// 约定：treap 的优先级是“大根堆”（优先级大者更靠近根）。
func insert(root *node, c Commit) *node {
	if root == nil {
		return &node{
			key:      c.BizStart,
			priority: priorityOf(c.BizStart),
			chain:    []int64{c.SysVersion},
		}
	}
	switch {
	case c.BizStart < root.key:
		child := insert(root.left, c)
		merged := cloneNode(root)
		merged.left = child
		if child.priority > root.priority {
			lr := child.right
			parent := cloneNode(child)
			parent.right = merged
			merged.left = lr
			return parent
		}
		return merged
	case c.BizStart > root.key:
		child := insert(root.right, c)
		merged := cloneNode(root)
		merged.right = child
		if child.priority > root.priority {
			rl := child.left
			parent := cloneNode(child)
			parent.left = merged
			merged.right = rl
			return parent
		}
		return merged
	default:
		// 同一业务起点：仅追加到覆盖链。路径拷贝保证旧快照仍指向旧切片。
		updated := cloneNode(root)
		updated.chain = make([]int64, len(root.chain)+1)
		copy(updated.chain, root.chain)
		updated.chain[len(root.chain)] = c.SysVersion
		return updated
	}
}

func cloneNode(n *node) *node {
	cp := *n
	return &cp
}

// lookupAt 在树中定位覆盖 bizAt 的最大起点，并返回该起点在 asOfSys 下
// 可见的系统版本号；visits 统计访问的节点数（可为 nil）。
func lookupAt(root *node, asOfSys, bizAt int64, visits *int) (int64, bool) {
	// 带可见性的 floor 查询：在 key <= bizAt 的节点里，取在 asOfSys 已
	// 提交（chain[0] <= asOfSys）的最大 key。经典 BST floor 的推广：
	// 每个节点最多访问一次，深度为树高，O(log n)；另加覆盖链上 O(log k)。
	best := (*node)(nil)
	cur := root
	for cur != nil {
		if visits != nil {
			*visits++
		}
		if bizAt < cur.key {
			cur = cur.left
			continue
		}
		if cur.chain[0] <= asOfSys {
			best = cur
			// 已提交：答案要么是本节点，要么在其右子树（更大 key）。
			cur = cur.right
			continue
		}
		// key <= bizAt 但该起点尚未提交：右子树可能有更大且已提交的
		// 起点，左子树可能有更小且已提交的起点。无法像普通 floor 那样
		// 单向剪枝，因此先探测右子树；命中即返回，否则落入左子树。
		// rightFloor 沿子树做同样的 floor 判定，总体仍只沿 O(树高) 的
		// 两条根到叶路径，访问次数为 O(log n)。
		if cand := floorVisible(cur.right, asOfSys, bizAt, visits); cand != nil {
			best = cand
			break
		}
		cur = cur.left
	}
	if best == nil {
		return 0, false
	}
	return chainAt(best.chain, asOfSys, visits)
}

// floorVisible 返回子树中 key<=bizAt 且在 asOfSys 已提交的最大节点。
func floorVisible(n *node, asOfSys, bizAt int64, visits *int) *node {
	for n != nil {
		if visits != nil {
			*visits++
		}
		if bizAt < n.key {
			n = n.left
			continue
		}
		if n.chain[0] <= asOfSys {
			if r := floorVisible(n.right, asOfSys, bizAt, visits); r != nil {
				return r
			}
			return n
		}
		if r := floorVisible(n.right, asOfSys, bizAt, visits); r != nil {
			return r
		}
		n = n.left
	}
	return nil
}

// chainAt 在升序的系统版本号链上返回不晚于 asOfSys 的最后一个；
// 不存在（整条链都晚于 asOfSys）时 ok=false。二分查找，O(log k)。
func chainAt(chain []int64, asOfSys int64, visits *int) (int64, bool) {
	lo, hi := 0, len(chain)
	for lo < hi {
		if visits != nil {
			*visits++
		}
		mid := int(uint(lo+hi) >> 1)
		if chain[mid] <= asOfSys {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo == 0 {
		return 0, false
	}
	return chain[lo-1], true
}

// priorityOf 返回某业务起点的确定性优先级。
// splitmix64 最终混合器：纯函数、雪崩性好，保证相同起点永远得到相同树结构。
func priorityOf(bizStart int64) uint64 {
	z := uint64(bizStart) + 0x9e3779b97f4a7c15
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

// inorderAsc 按业务起点升序收集节点。
func inorderAsc(root *node, out *[]*node) {
	if root == nil {
		return
	}
	inorderAsc(root.left, out)
	*out = append(*out, root)
	inorderAsc(root.right, out)
}
