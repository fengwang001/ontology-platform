package occupancy

// piece 是一条许可在索引中的一段有效区间（左闭右开）。
// 同一条许可在任意时刻至多有一段存在于活跃索引中。
type piece struct {
	key      int64
	permitID int64
	road     string
	start    int64
	end      int64
	lanes    int
	full     bool
	corr     string
	priority Priority
	approved bool
}

// node 是区间 treap 的节点。按 start 有序，maxEnd 为子树最大结束时刻，
// 可在点查/相交枚举时剪掉结束时刻不超过切点的整棵子树。
type node struct {
	p           piece
	left, right *node
	prio        uint64
	size        int
	maxEnd      int64
	minStart    int64
}

func (n *node) pull() {
	n.size = 1
	n.maxEnd = n.p.end
	n.minStart = n.p.start
	if n.left != nil {
		n.size += n.left.size
		if n.left.maxEnd > n.maxEnd {
			n.maxEnd = n.left.maxEnd
		}
		if n.left.minStart < n.minStart {
			n.minStart = n.left.minStart
		}
	}
	if n.right != nil {
		n.size += n.right.size
		if n.right.maxEnd > n.maxEnd {
			n.maxEnd = n.right.maxEnd
		}
		if n.right.minStart < n.minStart {
			n.minStart = n.right.minStart
		}
	}
}

func rotateRight(n *node) *node {
	x := n.left
	n.left = x.right
	x.right = n
	n.pull()
	x.pull()
	return x
}

func rotateLeft(n *node) *node {
	x := n.right
	n.right = x.left
	x.left = n
	n.pull()
	x.pull()
	return x
}

// piecePriority 由键派生确定性 treap 优先级，保证操作序列重放结果一致。
func piecePriority(key int64) uint64 {
	x := uint64(key) ^ 0x9e3779b97f4a7c15
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	x ^= x >> 31
	return x
}

func treapInsert(root *node, p piece) *node {
	if root == nil {
		return &node{p: p, prio: piecePriority(p.key), size: 1, maxEnd: p.end, minStart: p.start}
	}
	if lessPiece(p, root.p) {
		root.left = treapInsert(root.left, p)
		if root.left.prio > root.prio {
			root = rotateRight(root)
		}
	} else {
		root.right = treapInsert(root.right, p)
		if root.right.prio > root.prio {
			root = rotateLeft(root)
		}
	}
	root.pull()
	return root
}

// lessPiece 定义 treap 顺序：先按 start，再按唯一 key 兜底，保证严格全序。
func lessPiece(a, b piece) bool {
	if a.start != b.start {
		return a.start < b.start
	}
	return a.key < b.key
}

func treapDelete(root *node, p piece) *node {
	if root == nil {
		return nil
	}
	if lessPiece(p, root.p) {
		root.left = treapDelete(root.left, p)
	} else if lessPiece(root.p, p) {
		root.right = treapDelete(root.right, p)
	} else {
		switch {
		case root.left == nil:
			return root.right
		case root.right == nil:
			return root.left
		case root.left.prio > root.right.prio:
			root = rotateRight(root)
			root.right = treapDelete(root.right, p)
		default:
			root = rotateLeft(root)
			root.left = treapDelete(root.left, p)
		}
	}
	root.pull()
	return root
}

// appendIntersecting 枚举 root 中与 iv 相交的片段：
// 子树最小起点 >= iv.End 或最大结束 <= iv.Start 都可整棵剪掉。
func appendIntersecting(root *node, iv Interval, out []piece) []piece {
	if root == nil || root.maxEnd <= iv.Start || root.minStart >= iv.End {
		return out
	}
	out = appendIntersecting(root.left, iv, out)
	if iv.overlaps(Interval{root.p.start, root.p.end}) {
		out = append(out, root.p)
	}
	out = appendIntersecting(root.right, iv, out)
	return out
}

// roadIndex 是单条路段的区间存储。live 保留全部片段（含已结束的历史片段），
// treap 的 minStart/maxEnd 剪枝使点查只访问与 t 相关的节点；archive 保留
// 应急截断后作为历史事实的前段。查询复杂度不随历史许可总数增长。
type roadIndex struct {
	live     *node
	archive  *node
	nextKey  int64
	keyStart map[int64]int64
}

func newRoadIndex() *roadIndex {
	r := &roadIndex{nextKey: 1}
	r.keyStart = map[int64]int64{}
	return r
}

func (r *roadIndex) newKey() int64 {
	k := r.nextKey
	r.nextKey++
	return k
}

// insert 放入一段活跃区间，返回分配的唯一键。
func (r *roadIndex) insert(p piece) int64 {
	p.key = r.newKey()
	r.live = treapInsert(r.live, p)
	r.keyStart[p.key] = p.start
	return p.key
}

// archivePiece 直接把一段已成历史的区间写入归档（如抢占截断后保留的前段）。
func (r *roadIndex) archivePiece(p piece) {
	p.key = r.newKey()
	r.archive = treapInsert(r.archive, p)
}

// remove 从活跃索引删除指定片段（抢占撤销、失败重排、缩短重放前）。
func (r *roadIndex) remove(key int64) {
	start, ok := r.keyStart[key]
	if !ok {
		return
	}
	r.live = treapDeleteKey(r.live, key, start)
	delete(r.keyStart, key)
	// 堆采用懒删除：GC 时用 live 中的实际存在性校验。
}

// treapDeleteKey 按 (start,key) 全序沿 BST 删除。
func treapDeleteKey(root *node, key, start int64) *node {
	if root == nil {
		return nil
	}
	target := piece{start: start, key: key}
	if !lessPiece(root.p, target) && !lessPiece(target, root.p) {
		switch {
		case root.left == nil:
			return root.right
		case root.right == nil:
			return root.left
		case root.left.prio > root.right.prio:
			root = rotateRight(root)
			root.right = treapDeleteKey(root.right, key, start)
		default:
			root = rotateLeft(root)
			root.left = treapDeleteKey(root.left, key, start)
		}
		root.pull()
		return root
	}
	if lessPiece(target, root.p) {
		root.left = treapDeleteKey(root.left, key, start)
	} else {
		root.right = treapDeleteKey(root.right, key, start)
	}
	root.pull()
	return root
}

// activeAtOrAfter 返回结束时刻 > t 的现存片段数，即点查 t 的输出规模上界。
// 通过扫描 treap 并利用 maxEnd 剪枝，复杂度与历史总数无关。
func (r *roadIndex) activeAtOrAfter(t int64) int {
	return countEndAfter(r.live, t)
}

func countEndAfter(root *node, t int64) int {
	if root == nil || root.maxEnd <= t {
		return 0
	}
	n := countEndAfter(root.left, t)
	if root.p.end > t {
		n++
	}
	n += countEndAfter(root.right, t)
	return n
}

func findPiece(root *node, key int64) (piece, bool) {
	for root != nil {
		if key < root.p.key {
			root = root.left
		} else if key > root.p.key {
			root = root.right
		} else {
			return root.p, true
		}
	}
	return piece{}, false
}

// intersecting 返回活跃片段中与 iv 相交者。
func (r *roadIndex) intersecting(iv Interval) []piece {
	return appendIntersecting(r.live, iv, nil)
}

// pointAt 返回在时刻 t 生效的片段。treap 按 start 有序，点查沿 BST 下降到
// 最后一个 start<=t 的节点（O(log N)），其路径上“挂向右侧”的左子树才可能
// 包含更早开始的片段；这些左子树再用 maxEnd<=t 整棵剪掉。因此访问节点数
// 为 O(log N + k)，其中 k 是 end>t 的片段数，与已结束历史总数无关。
func (r *roadIndex) pointAt(t int64) []piece {
	var out []piece
	out = pointCollect(r.live, t, out)
	out = pointCollect(r.archive, t, out)
	return out
}

// pointCollect 收集 start<=t<end 的片段。
//
// 遍历规则（按 start 的 BST）：
//   - 当前节点 start<=t：它自身可能命中；其左子树也可能命中（左子树起点更小）；
//     右子树起点更大，下降继续搜索。
//   - 当前节点 start>t：自身与右子树都不可能命中，只进左子树。
//
// 对任何“整棵左子树”，用 maxEnd<=t 剪枝；右子树的下降路径长度 O(log N)。
func pointCollect(root *node, t int64, out []piece) []piece {
	if root == nil {
		return out
	}
	if root.p.start <= t {
		if t < root.p.end {
			out = append(out, root.p)
		}
		if root.left != nil && root.left.maxEnd > t {
			out = collectLeft(root.left, t, out)
		}
		out = pointCollect(root.right, t, out)
	} else {
		out = pointCollect(root.left, t, out)
	}
	return out
}

// collectLeft 收集一个“已知全部 start<=t”的子树里 t 时刻生效的片段；
// 用 maxEnd<=t 整棵剪掉，访问量与该子树内 end>t 的片段数同阶。
func collectLeft(root *node, t int64, out []piece) []piece {
	if root == nil || root.maxEnd <= t {
		return out
	}
	if root.p.start <= t && t < root.p.end {
		out = append(out, root.p)
	}
	out = collectLeft(root.left, t, out)
	if root.p.start <= t {
		out = collectLeft(root.right, t, out)
	}
	return out
}

// liveCount 仅供测试：treap 中现存片段总数。
func (r *roadIndex) liveCount() int {
	if r.live == nil {
		return 0
	}
	return r.live.size
}
