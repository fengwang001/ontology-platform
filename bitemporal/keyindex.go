package bitemporal

import (
	"sort"

	"ontology/core"
)

// node 是按业务时间起点组织的 treap 节点。
//
// 每个节点维护子树内 firstSeq 的最小值 minSeq，使得
// 「as-of 系统时间 sysQ 下该起点是否已有记录」可以在 O(1) 内裁剪，
// 从而把点查询的代价压到树高量级（期望 O(log V)）。
// priority 由 bizStart 确定性派生，重放同一操作序列时树的形态一致。
type node struct {
	bizStart int64
	firstSeq uint64 // 该业务起点首次出现的系统时间序号
	priority uint64
	minSeq   uint64 // 子树内 firstSeq 的最小值
	left     *node
	right    *node
}

func (n *node) refresh() {
	n.minSeq = n.firstSeq
	if n.left != nil && n.left.minSeq < n.minSeq {
		n.minSeq = n.left.minSeq
	}
	if n.right != nil && n.right.minSeq < n.minSeq {
		n.minSeq = n.right.minSeq
	}
}

func rotateRight(n *node) *node {
	l := n.left
	n.left = l.right
	l.right = n
	n.refresh()
	l.refresh()
	return l
}

func rotateLeft(n *node) *node {
	r := n.right
	n.right = r.left
	r.left = n
	n.refresh()
	r.refresh()
	return r
}

// priorityOf 用 splitmix64 从 bizStart 确定性派生优先级。
func priorityOf(bizStart int64) uint64 {
	z := uint64(bizStart) + 0x9e3779b97f4a7c15
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

func insert(n *node, bizStart int64, firstSeq uint64) *node {
	if n == nil {
		return &node{bizStart: bizStart, firstSeq: firstSeq, priority: priorityOf(bizStart), minSeq: firstSeq}
	}
	if bizStart < n.bizStart {
		n.left = insert(n.left, bizStart, firstSeq)
		if n.left.priority < n.priority {
			n = rotateRight(n)
		}
	} else {
		n.right = insert(n.right, bizStart, firstSeq)
		if n.right.priority < n.priority {
			n = rotateLeft(n)
		}
	}
	n.refresh()
	return n
}

// findCovering 返回满足「firstSeq <= sysQ 且 bizStart <= bizQ」的最大
// bizStart 节点，即 as-of sysQ 时业务时间区间覆盖 bizQ 的那个起点。
// steps 记录访问的节点数，用于复杂度验证。
//
// 代价分析：沿根下行时，若某子树内不存在 firstSeq <= sysQ 的节点，
// minSeq 裁剪使其在 O(1) 内被跳过；每层至多跟随一条通向目标的路径，
// 总访问节点数为 O(树高)，treap 期望树高 O(log n)。
func findCovering(n *node, bizQ int64, sysQ uint64, steps *int) *node {
	if n == nil {
		return nil
	}
	*steps++
	if n.minSeq > sysQ {
		return nil
	}
	if n.bizStart > bizQ {
		return findCovering(n.left, bizQ, sysQ, steps)
	}
	if r := findCovering(n.right, bizQ, sysQ, steps); r != nil {
		return r
	}
	if n.firstSeq <= sysQ {
		return n
	}
	return findCovering(n.left, bizQ, sysQ, steps)
}

// keyIndex 是单个主键的双时态索引。
type keyIndex struct {
	bySeq []core.Version     // 按 Seq 递增的完整版本链副本（Seq == 下标+1）
	byBiz map[int64][]uint64 // 业务起点 -> 该起点下递增的 Seq 列表（等起点覆盖关系在此仲裁）
	root  *node              // 业务起点 treap
}

func newKeyIndex() *keyIndex {
	return &keyIndex{byBiz: make(map[int64][]uint64)}
}

// apply 追加一条版本，必须满足 v.Seq == len(bySeq)+1。
func (ki *keyIndex) apply(v core.Version) {
	ki.bySeq = append(ki.bySeq, v)
	seqs := ki.byBiz[v.BizStart]
	if seqs == nil {
		ki.root = insert(ki.root, v.BizStart, v.Seq)
	}
	ki.byBiz[v.BizStart] = append(seqs, v.Seq)
}

// latestAtOrBefore 返回该业务起点下 Seq <= sysQ 的最大 Seq（必然存在，
// 因为调用前已确认 firstSeq <= sysQ）。steps 计入二分比较次数。
func latestAtOrBefore(seqs []uint64, sysQ uint64, steps *int) uint64 {
	idx := sort.Search(len(seqs), func(i int) bool {
		*steps++
		return seqs[i] > sysQ
	}) - 1
	return seqs[idx]
}

// query 实现点查询，返回结果与探测步数。
func (ki *keyIndex) query(sysQ uint64, bizQ int64) (core.QueryResult, int) {
	steps := 0
	n := findCovering(ki.root, bizQ, sysQ, &steps)
	if n == nil {
		return core.QueryResult{Visibility: core.NotVisible}, steps
	}
	seq := latestAtOrBefore(ki.byBiz[n.bizStart], sysQ, &steps)
	v := ki.bySeq[seq-1]
	res := core.QueryResult{Version: &v}
	if v.Deleted {
		res.Visibility = core.Deleted
	} else {
		res.Visibility = core.Found
	}
	return res, steps
}

// rebuildAsOf 中序遍历收集 as-of sysQ 可见的业务起点，重建完整时间线。
// 代价与输出区间数成正比（历史重建本身是线性输出）。
func (ki *keyIndex) rebuildAsOf(sysQ uint64) []core.Interval {
	var nodes []*node
	var walk func(n *node)
	walk = func(n *node) {
		if n == nil {
			return
		}
		walk(n.left)
		if n.firstSeq <= sysQ {
			nodes = append(nodes, n)
		}
		walk(n.right)
	}
	walk(ki.root)

	out := make([]core.Interval, 0, len(nodes))
	for i, n := range nodes {
		steps := 0
		seq := latestAtOrBefore(ki.byBiz[n.bizStart], sysQ, &steps)
		iv := core.Interval{Start: n.bizStart, Version: ki.bySeq[seq-1]}
		if i+1 < len(nodes) {
			iv.End = nodes[i+1].bizStart
		} else {
			iv.Open = true
		}
		out = append(out, iv)
	}
	return out
}
