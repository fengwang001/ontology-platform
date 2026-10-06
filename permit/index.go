package permit

import "encoding/binary"

// indexNode 是按 (Start, ID) 为键、支持子树聚合的确定性 treap 节点。
// 树中保留该键空间内所有"曾受理"许可的当前版本节点：
//   - 已结束许可的节点不做物理删除——查询任意历史时刻仍须精确复现；
//   - 撤销的许可以 valid=false 的墓碑节点保留，不参与任何判定与查询；
//   - 抢占顺延/延期导致 start 改变时，旧 start 键节点随新键插入，
//     旧节点由 permits 表中的版本标记为无效（valid=false）。
//
// 相交枚举利用子树 maxEnd 剪枝与键序下界，复杂度 O(log n + 命中数)，
// 与该键空间内的历史许可总数无关（历史节点因 maxEnd 剪枝不会被访问）。
type indexNode struct {
	start    Time
	end      Time
	lanes    int
	id       string
	valid    bool
	priority uint64
	left     *indexNode
	right    *indexNode

	size   int
	maxEnd Time
}

type activeIndex struct {
	root *indexNode
}

func keyLess(start Time, id string, ostart Time, oid string) bool {
	if start != ostart {
		return start < ostart
	}
	return id < oid
}

// detPriority 由 (start,id) 派生确定性 treap 优先级，保证重放结果一致。
func detPriority(start Time, id string) uint64 {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:8], uint64(start))
	h := uint64(1469598103934665603)
	for _, b := range []byte(id) {
		h ^= uint64(b)
		h *= 1099511628211
	}
	h ^= binary.LittleEndian.Uint64(buf[:8])
	h *= 1099511628211
	h ^= h >> 33
	return h
}

func newIndexNode(p *Permit) *indexNode {
	n := &indexNode{
		start: p.indexedStart, end: p.Current.End, lanes: p.Lanes,
		id: p.ID, valid: true, priority: detPriority(p.indexedStart, p.ID),
	}
	n.pull()
	return n
}

func (n *indexNode) pull() {
	n.size = 1
	n.maxEnd = n.end
	if n.left != nil {
		n.size += n.left.size
		if n.left.maxEnd > n.maxEnd {
			n.maxEnd = n.left.maxEnd
		}
	}
	if n.right != nil {
		n.size += n.right.size
		if n.right.maxEnd > n.maxEnd {
			n.maxEnd = n.right.maxEnd
		}
	}
}

func rotateRight(n *indexNode) *indexNode {
	l := n.left
	n.left = l.right
	l.right = n
	n.pull()
	l.pull()
	return l
}

func rotateLeft(n *indexNode) *indexNode {
	r := n.right
	n.right = r.left
	r.left = n
	n.pull()
	r.pull()
	return r
}

func (t *activeIndex) insert(n *indexNode) { t.root = t.insertAt(t.root, n) }

// upsert 插入节点；若同 (start,id) 节点已存在则原地更新内容并复活。
// 缩短延期/同键重入时必须走 upsert，避免同键产生两个节点。
func (t *activeIndex) upsert(n *indexNode) {
	if found := t.find(t.root, n.start, n.id); found != nil {
		found.end = n.end
		found.lanes = n.lanes
		found.valid = true
		// 原地更新 end 后必须沿根路径重算 maxEnd 等聚合值。
		t.recomputePath(n.start, n.id)
		return
	}
	t.insert(n)
}

// recomputePath 自叶到根重算键 (start,id) 所在路径各节点的聚合值。
func (t *activeIndex) recomputePath(start Time, id string) {
	var path []*indexNode
	cur := t.root
	for cur != nil {
		path = append(path, cur)
		if keyLess(start, id, cur.start, cur.id) {
			cur = cur.left
		} else if keyLess(cur.start, cur.id, start, id) {
			cur = cur.right
		} else {
			break
		}
	}
	for i := len(path) - 1; i >= 0; i-- {
		path[i].pull()
	}
}

func (t *activeIndex) find(root *indexNode, start Time, id string) *indexNode {
	if root == nil {
		return nil
	}
	if keyLess(start, id, root.start, root.id) {
		return t.find(root.left, start, id)
	}
	if keyLess(root.start, root.id, start, id) {
		return t.find(root.right, start, id)
	}
	return root
}

func (t *activeIndex) insertAt(root, n *indexNode) *indexNode {
	if root == nil {
		return n
	}
	if keyLess(n.start, n.id, root.start, root.id) {
		root.left = t.insertAt(root.left, n)
		if root.left.priority > root.priority {
			root = rotateRight(root)
		}
	} else {
		root.right = t.insertAt(root.right, n)
		if root.right.priority > root.priority {
			root = rotateLeft(root)
		}
	}
	root.pull()
	return root
}

// invalidate 把键 (start,id) 的节点标记为墓碑（撤销/重定位旧版本）。
func (t *activeIndex) invalidate(start Time, id string) {
	t.setValid(start, id, false)
}

// setValid 翻转键 (start,id) 节点的墓碑标志。
func (t *activeIndex) setValid(start Time, id string, valid bool) {
	var walk func(n *indexNode)
	walk = func(n *indexNode) {
		if n == nil {
			return
		}
		if keyLess(start, id, n.start, n.id) {
			walk(n.left)
			return
		}
		if keyLess(n.start, n.id, start, id) {
			walk(n.right)
			return
		}
		n.valid = valid
	}
	walk(t.root)
}

func (t *activeIndex) count() int {
	if t.root == nil {
		return 0
	}
	return t.root.size
}

// overlaps 枚举与 iv 相交且仍有效的节点。
// maxEnd 剪枝：子树最大 End <= iv.Start 时整棵跳过（首尾相接不算相交）；
// 键序剪枝：进入 Start >= iv.End 的键域后不再深入。
func (t *activeIndex) overlaps(iv Interval) []*indexNode {
	var out []*indexNode
	var walk func(n *indexNode)
	walk = func(n *indexNode) {
		if n == nil || n.maxEnd <= iv.Start {
			return
		}
		if n.left != nil {
			walk(n.left)
		}
		if n.start >= iv.End {
			return
		}
		if n.valid && (Interval{Start: n.start, End: n.end}).overlap(iv) {
			out = append(out, n)
		}
		walk(n.right)
	}
	walk(t.root)
	return out
}

func (t *activeIndex) activeAt(at Time) []*indexNode {
	return t.overlaps(Interval{Start: at, End: at + 1})
}
