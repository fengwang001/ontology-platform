package gatealloc

// tree.go：每个登机口一棵按区间起点排序的增广 treap。
//
// 复杂度（n 为该登机口历史/当前总占用数，k 为与查询区间相交的占用数）：
//   - 插入、删除：期望 O(log n)；
//   - Overlaps（与 [start,end) 相交的全部占用）：期望 O(log n + k)，
//     通过节点子树 maxEnd 剪枝，不相交且其后也不可能相交的整棵子树被跳过；
//   - Occupant（某时刻占用者）：Overlaps 的 k 恒为 0/1，期望 O(log n)。
// 因此单次指派只需查询目标登机口与其相邻登机口，开销不随航班总数增长；
// 某时刻占用者查询不随历史占用总数增长（树中删除的节点不保留历史）。

// treeOcc 是区间树中保存的一条占用。
type treeOcc struct {
	flight string
	seg    Segment
	start  int
	end    int
}

// treeKey 定义全序：起点升序，再按航班标识、段，保证结果确定。
func treeLess(a, b *treeOcc) bool {
	if a.start != b.start {
		return a.start < b.start
	}
	if a.flight != b.flight {
		return a.flight < b.flight
	}
	return a.seg < b.seg
}

type treapNode struct {
	occ         *treeOcc
	left, right *treapNode
	priority    uint64
	maxEnd      int // 本节点及子树中的最大右端
}

func (t *treapNode) pull() {
	t.maxEnd = t.occ.end
	if t.left != nil && t.left.maxEnd > t.maxEnd {
		t.maxEnd = t.left.maxEnd
	}
	if t.right != nil && t.right.maxEnd > t.maxEnd {
		t.maxEnd = t.right.maxEnd
	}
}

// rotRight / rotLeft 保持 maxEnd 增广正确。
func rotRight(h *treapNode) *treapNode {
	x := h.left
	h.left = x.right
	x.right = h
	h.pull()
	x.pull()
	return x
}

func rotLeft(h *treapNode) *treapNode {
	x := h.right
	h.right = x.left
	x.left = h
	h.pull()
	x.pull()
	return x
}

// occTree 是单登机口的区间索引。
type occTree struct {
	root *treapNode
	// probes 统计最近一次 overlaps 访问的节点数，供性能验证。
	probes int
}

// splitKey 是确定性优先级：同一键始终得到同一优先级，重放一致。
func splitKey(o *treeOcc) uint64 {
	const (
		m  uint64 = 0x9e3779b97f4a7c15
		m2 uint64 = 0xc2b2ae3d27d4eb4f
	)
	h := m ^ uint64(o.start)*m2
	h ^= uint64(o.end) * 0x100000001b3
	h ^= uint64(o.seg) * 0x9e3779b9
	for i := 0; i < len(o.flight); i++ {
		h ^= uint64(o.flight[i]) * m
		h = (h << 13) | (h >> 51)
	}
	h ^= h >> 31
	h *= m
	h ^= h >> 27
	h *= m2
	h ^= h >> 33
	return h
}

func (t *occTree) insert(o *treeOcc) {
	t.root = t.insertNode(t.root, &treapNode{occ: o, priority: splitKey(o), maxEnd: o.end})
}

func (t *occTree) insertNode(h, n *treapNode) *treapNode {
	if h == nil {
		return n
	}
	if treeLess(n.occ, h.occ) {
		h.left = t.insertNode(h.left, n)
		if h.left.priority > h.priority {
			h = rotRight(h)
		}
	} else {
		h.right = t.insertNode(h.right, n)
		if h.right.priority > h.priority {
			h = rotLeft(h)
		}
	}
	h.pull()
	return h
}

func (t *occTree) delete(flight string, seg Segment, start int) {
	key := &treeOcc{flight: flight, seg: seg, start: start}
	t.root = t.deleteNode(t.root, key)
}

func (t *occTree) deleteNode(h *treapNode, key *treeOcc) *treapNode {
	if h == nil {
		return nil
	}
	switch {
	case treeLess(key, h.occ):
		h.left = t.deleteNode(h.left, key)
	case treeLess(h.occ, key):
		h.right = t.deleteNode(h.right, key)
	default:
		switch {
		case h.left == nil:
			return h.right
		case h.right == nil:
			return h.left
		default:
			if h.left.priority > h.right.priority {
				h = rotRight(h)
				h.right = t.deleteNode(h.right, key)
			} else {
				h = rotLeft(h)
				h.left = t.deleteNode(h.left, key)
			}
		}
	}
	h.pull()
	return h
}

// overlaps 返回与 [start,end) 相交的占用，按全序（起点最早、标识最小）排列。
func (t *occTree) overlaps(start, end int) []*treeOcc {
	t.probes = 0
	var out []*treeOcc
	var walk func(h *treapNode)
	walk = func(h *treapNode) {
		if h == nil {
			return
		}
		t.probes++
		// 左子树所有区间起点不大于当前起点；若其 maxEnd 不大于 start，
		// 整棵左子树都不可能与查询区间相交，直接剪枝。
		if h.left != nil && h.left.maxEnd > start {
			walk(h.left)
		}
		if h.occ.start >= end {
			return // 当前及右子树起点均不小于 end，剪枝
		}
		if h.occ.end > start {
			out = append(out, h.occ)
		}
		walk(h.right)
	}
	walk(t.root)
	return out
}

// Probes 返回最近一次 overlaps 访问的节点数。
func (t *occTree) Probes() int { return t.probes }

// size 统计当前占用数（仅测试/快照使用）。
func (t *occTree) size() int {
	var n int
	var walk func(*treapNode)
	walk = func(h *treapNode) {
		if h == nil {
			return
		}
		n++
		walk(h.left)
		walk(h.right)
	}
	walk(t.root)
	return n
}
