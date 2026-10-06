package docsync

import "sort"

// dEntry 是诊断在索引树中的负载。start/end 为当前版本坐标（码元偏移）。
type dEntry struct {
	key      int // 所在树的排序键：byStart 树为 start，byEnd 树为 end
	start    int
	end      int
	sequence int
	severity Severity
	message  string
}

// dNode 是诊断 treap 节点。delta 是作用于整棵子树全部键与坐标的懒平移。
type dNode struct {
	entry       dEntry
	left, right *dNode
	priority    uint64
	delta       int
	minStart    int // 子树内最小 start
	maxKey      int // 子树内最大排序键
	treeRole    int // 0=byStart（移动 key/start），1=byEnd（移动 key/end）
}

func dPull(n *dNode) *dNode {
	n.minStart = n.entry.start
	n.maxKey = n.entry.key
	if n.left != nil {
		n.minStart = min(n.minStart, n.left.minStart)
		n.maxKey = max(n.maxKey, n.left.maxKey)
	}
	if n.right != nil {
		n.minStart = min(n.minStart, n.right.minStart)
		n.maxKey = max(n.maxKey, n.right.maxKey)
	}
	return n
}

// dApply 对整棵子树施加平移 d（键、start、end 同步移动）。
func dApply(n *dNode, d int) *dNode {
	if n == nil || d == 0 {
		return n
	}
	e := n.entry
	e.key += d
	e.start += d
	e.end += d
	n.entry = e
	n.delta += d
	n.minStart += d
	n.maxKey += d
	return n
}

func dPush(n *dNode) *dNode {
	if n != nil && n.delta != 0 {
		n.left = dApply(n.left, n.delta)
		n.right = dApply(n.right, n.delta)
		n.delta = 0
	}
	return n
}

func cmpKey(key, seq, k2, s2 int) int {
	if key != k2 {
		if key < k2 {
			return -1
		}
		return 1
	}
	if seq != s2 {
		if seq < s2 {
			return -1
		}
		return 1
	}
	return 0
}

// dMerge 按完整键 (key,sequence) 保序合并两棵 BST。标准 treap merge 要求
// a 的全部键 <= b；当平移/重插使两树在编辑点出现同键交叉时，用按
// (key,seq) 的分裂把越界部分归位，从而始终维持严格 BST 序。
func dMerge(a, b *dNode) *dNode {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if a.priority > b.priority {
		dPush(a)
		// 以 a 的键为界，把 b 分成 (<aKey, >aKey) 两部分（(key,seq) 相等不存在，
		// 因为 (key,seq) 全局唯一）。
		bLo, bHi := dSplit(b, a.entry.key, a.entry.sequence, false)
		a.left = dMerge(a.left, bLo)
		a.right = dMerge(a.right, bHi)
		return dPull(a)
	}
	dPush(b)
	aLo, aHi := dSplit(a, b.entry.key, b.entry.sequence, true)
	b.left = dMerge(aLo, b.left)
	b.right = dMerge(aHi, b.right)
	return dPull(b)
}

// dSplit 按 (key,seq) 分裂：左 <= (key,seq) < 右（equalLeft=true 时等于归左）。
func dSplit(n *dNode, key, seq int, equalLeft bool) (*dNode, *dNode) {
	if n == nil {
		return nil, nil
	}
	dPush(n)
	c := cmpKey(n.entry.key, n.entry.sequence, key, seq)
	goLeft := c < 0 || (equalLeft && c == 0)
	if goLeft {
		a, b := dSplit(n.right, key, seq, equalLeft)
		clone := *n
		clone.right = a
		return dPull(&clone), b
	}
	a, b := dSplit(n.left, key, seq, equalLeft)
	clone := *n
	clone.left = b
	return a, dPull(&clone)
}

// dSplitLess 按单键分裂：左 key < v，右 key >= v。
func dSplitLess(n *dNode, v int) (*dNode, *dNode) {
	if n == nil {
		return nil, nil
	}
	dPush(n)
	if n.entry.key < v {
		a, b := dSplitLess(n.right, v)
		clone := *n
		clone.right = a
		return dPull(&clone), b
	}
	a, b := dSplitLess(n.left, v)
	clone := *n
	clone.left = b
	return a, dPull(&clone)
}

// dSplitLE 按单键分裂：左 key <= v，右 key > v。
func dSplitLE(n *dNode, v int) (*dNode, *dNode) {
	if n == nil {
		return nil, nil
	}
	dPush(n)
	if n.entry.key <= v {
		a, b := dSplitLE(n.right, v)
		clone := *n
		clone.right = a
		return dPull(&clone), b
	}
	a, b := dSplitLE(n.left, v)
	clone := *n
	clone.left = b
	return a, dPull(&clone)
}

func dInsert(root *dNode, e dEntry) *dNode {
	n := &dNode{entry: e, priority: nextPriority()}
	if root != nil {
		n.treeRole = root.treeRole
	}
	dPull(n)
	a, b := dSplit(root, e.key, e.sequence, true)
	return dMerge(dMerge(a, n), b)
}

// dInsertRole 向指定角色的树插入（根为空时使用）。
func dInsertRole(root *dNode, e dEntry, role int) *dNode {
	n := &dNode{entry: e, priority: nextPriority(), treeRole: role}
	dPull(n)
	a, b := dSplit(root, e.key, e.sequence, true)
	return dMerge(dMerge(a, n), b)
}

// dDeleteSeq 删除排序键为 key、登记号为 seq 的节点。
func dDeleteSeq(root *dNode, key, seq int) *dNode {
	a, rest := dSplit(root, key, seq, true)
	a, dropped := dSplit(a, key, seq, false)
	_ = dropped
	return dMerge(a, rest)
}

// addDeltaRange 对排序键落在 [lo,hi) 的所有节点整体加 d。
// 存活端点映射是全局单调不减函数，平移后“左段/中段/右段”的新键
// 仍然有序（允许在编辑点相等），故三段直接有序合并即可。
func addDeltaRange(root *dNode, lo, hi, d int) *dNode {
	if d == 0 || lo >= hi {
		return root
	}
	a, rest := dSplitLess(root, lo)
	mid, c := dSplitLess(rest, hi)
	mid = dApply(mid, d)
	return dMerge(dMerge(a, mid), c)
}

// addDeltaPoint 对排序键恰好等于 p 的所有节点整体加 d。
func addDeltaPoint(root *dNode, p, d int) *dNode {
	if d == 0 {
		return root
	}
	a, rest := dSplitLess(root, p)
	mid, c := dSplitLE(rest, p)
	mid = dApply(mid, d)
	return dMerge(dMerge(a, mid), c)
}

// dMutate 按键 (key,seq) 摘出节点，用 fn 修改其 entry（不改 key），再放回。
func dMutate(root *dNode, key, seq int, fn func(e dEntry) dEntry) *dNode {
	a, rest := dSplit(root, key, seq, true)
	a2, mid := dSplit(a, key, seq, false)
	if mid != nil {
		mid.entry = fn(mid.entry)
		dPull(mid)
	}
	return dMerge(dMerge(a2, mid), rest)
}

// dMoveKey 摘出键为 (oldKey,seq) 的节点，按 fn 计算新键并放回（保持角色与优先级）。
func dMoveKey(root *dNode, oldKey, seq int, fn func(e dEntry) (dEntry, int)) *dNode {
	a, rest := dSplit(root, oldKey, seq, true)
	a2, mid := dSplit(a, oldKey, seq, false)
	if mid == nil {
		return dMerge(dMerge(a2, mid), rest)
	}
	role := mid.treeRole
	ne, _ := fn(mid.entry)
	mid.entry = ne
	mid.left, mid.right, mid.delta = nil, nil, 0
	mid.treeRole = role
	dPull(mid)
	return dMerge(dMerge(a2, rest), mid)
}

// except 是插入/替换中需要逐个修正的例外诊断。
type except struct {
	sequence int
	start    int
	end      int
	kind     int // 1=straddler 2=emptyAt 3=repBoundary
}

// enumerateInsertExceptions 在 byEnd 树上枚举插入点 s 的例外：
// 跨点诊断 start<s<end（kind1）与空诊断 start==end==s（kind2）。
// 剪枝：maxKey<=s 或 minStart>=s 的子树整体无例外。
func enumerateInsertExceptions(n *dNode, s int, out []except) []except {
	if n == nil {
		return out
	}
	// 可能命中的两类：跨点 start<s<end（需 maxKey>s 且 minStart<s），
	// 以及空诊断 start==end==s（需 maxKey>=s 且 minStart<=s）。
	if n.maxKey < s || n.minStart > s {
		return out
	}
	dPush(n)
	e := n.entry
	if e.start < s && e.end > s {
		out = append(out, except{sequence: e.sequence, start: e.start, end: e.end, kind: 1})
	} else if e.start == s && e.end == s {
		out = append(out, except{sequence: e.sequence, start: s, end: s, kind: 2})
	}
	out = enumerateInsertExceptions(n.left, s, out)
	out = enumerateInsertExceptions(n.right, s, out)
	return out
}

// enumerateRepBoundary 在 byEnd 树上枚举 start==s 且 end>=e 的边界存活诊断。
// 剪枝：maxKey<e 或 minStart>s 的子树无此诊断。
func enumerateRepBoundary(n *dNode, s, e int, out []except) []except {
	if n == nil {
		return out
	}
	if n.maxKey < e || n.minStart > s {
		return out
	}
	dPush(n)
	en := n.entry
	if en.start == s && en.end >= e {
		out = append(out, except{sequence: en.sequence, start: s, end: en.end, kind: 3})
	}
	out = enumerateRepBoundary(n.left, s, e, out)
	out = enumerateRepBoundary(n.right, s, e, out)
	return out
}

// deadHit 描述一个失效诊断命中的信息。
type deadHit struct {
	sequence int
	start    int
	end      int
}

// collectDead 在 byEnd 树上枚举与非空编辑 [s,e) 相交的诊断。
// 剪枝：子树最大 end<=s（b<=s）或子树最小 start>=e（a>=e）整体不相交。
// 只下钻可能相交的子树；被剪枝子树仅读聚合字段。
func collectDead(n *dNode, s, e int, out []deadHit) []deadHit {
	if n == nil {
		return out
	}
	if n.maxKey <= s || n.minStart >= e {
		return out
	}
	dPush(n)
	en := n.entry
	if en.key > s && en.start < e {
		empty := en.start == en.end
		strictInside := empty && en.start > s && en.start < e
		if !empty || strictInside {
			out = append(out, deadHit{sequence: en.sequence, start: en.start, end: en.end})
		}
	}
	out = collectDead(n.left, s, e, out)
	out = collectDead(n.right, s, e, out)
	return out
}

// dInorder 按键升序（同键按登记号）收集全部 entry（快照查询用）。
func dInorder(n *dNode, out []dEntry) []dEntry {
	if n == nil {
		return out
	}
	dPush(n)
	out = dInorder(n.left, out)
	out = append(out, n.entry)
	out = dInorder(n.right, out)
	return out
}

// deadList 是已失效清单，按 (失效版本, 登记号) 排序输出。
type deadList []deadRecord

type deadRecord struct {
	rec           deadDiagnostic
	failedVersion int
}

func (dl deadList) sorted() []deadDiagnostic {
	out := make([]deadDiagnostic, len(dl))
	for i, r := range dl {
		r.rec.failedVersion = r.failedVersion
		out[i] = r.rec
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].failedVersion != out[j].failedVersion {
			return out[i].failedVersion < out[j].failedVersion
		}
		return out[i].sequence < out[j].sequence
	})
	return out
}
