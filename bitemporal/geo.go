package bitemporal

import "sort"

// 本文件实现两类静态只读索引：
//
//  1. rectIndex：二维矩形穿刺索引，回答“(recordTime, validTime) 处哪些物理半存活”。
//     每个半把其“获胜事实”分解为至多与其事实数相等的矩形（总矩形数 O(F)），
//     在记录时间刻度上做线段树规范分解，节点内是有效时间中心区间树。
//     点查询开销 O(log^2 F + k)，k 为命中半数，不随 F 线性增长。
//  2. intervalIndex：一维区间穿刺索引（中心区间树），
//     用于按记录时间窗口枚举违反活动区间 / 结构缺陷区间，开销 O(log F + k)。

const vtInf = 1 << 62

type rect struct {
	rt0, rt1 int64
	vt0, vt1 int64
	alive    bool
	val      int // 编码后的物理半（halfValue）
	order    int // 获胜事实的全顺序位置，用于同点决胜
}

type ivl struct {
	lo, hi int64
	val    int
}

type vtNode struct {
	center  int64
	ascEnd  []rect // 覆盖 center 的矩形，按 vt1 升序
	descBeg []rect // 覆盖 center 的矩形，按 vt0 降序
	left    *vtNode
	right   *vtNode
}

type segNode struct {
	tree *vtNode
}

type rectIndex struct {
	rtTicks []int64
	tree    []segNode // 数组式线段树，长度 2*n
	probe   *ProbeCounts
}

func newRectIndex(rtTicks []int64, rects []rect, probe *ProbeCounts) *rectIndex {
	idx := &rectIndex{rtTicks: rtTicks, probe: probe}
	if len(rtTicks) == 0 {
		return idx
	}
	n := 1
	for n < len(rtTicks) {
		n <<= 1
	}
	idx.tree = make([]segNode, 2*n)
	buckets := make([][]rect, 2*n)
	for _, r := range rects {
		// 叶子 i 覆盖 [ticks[i], ticks[i+1])。
		// l = 首个 ticks[i] >= rt0；u = 首个 ticks[i] >= rt1。
		l := sort.Search(len(rtTicks), func(i int) bool { return rtTicks[i] >= r.rt0 })
		u := sort.Search(len(rtTicks), func(i int) bool { return rtTicks[i] >= r.rt1 })
		if l >= u {
			continue
		}
		l += n
		u += n
		for l < u {
			if l&1 != 0 {
				buckets[l] = append(buckets[l], r)
				l++
			}
			if u&1 != 0 {
				u--
				buckets[u] = append(buckets[u], r)
			}
			l >>= 1
			u >>= 1
		}
	}
	for i := 1; i < 2*n; i++ {
		if len(buckets[i]) > 0 {
			idx.tree[i].tree = buildVT(buckets[i])
		}
	}
	return idx
}

func buildVT(rs []rect) *vtNode {
	if len(rs) == 0 {
		return nil
	}
	bounds := make([]int64, 0, len(rs))
	for _, r := range rs {
		bounds = append(bounds, r.vt0)
		if r.vt1 != vtInf {
			bounds = append(bounds, r.vt1-1)
		}
	}
	sort.Slice(bounds, func(i, j int) bool { return bounds[i] < bounds[j] })
	bounds = dedupInts(bounds)
	center := bounds[len(bounds)/2]

	var here, lefts, rights []rect
	for _, r := range rs {
		if r.vt0 <= center && center < r.vt1 {
			here = append(here, r)
		} else if r.vt1 <= center {
			lefts = append(lefts, r)
		} else {
			rights = append(rights, r)
		}
	}
	vn := &vtNode{center: center}
	vn.ascEnd = append([]rect{}, here...)
	sort.Slice(vn.ascEnd, func(i, j int) bool { return vn.ascEnd[i].vt1 < vn.ascEnd[j].vt1 })
	vn.descBeg = append([]rect{}, here...)
	sort.Slice(vn.descBeg, func(i, j int) bool {
		if vn.descBeg[i].vt0 != vn.descBeg[j].vt0 {
			return vn.descBeg[i].vt0 > vn.descBeg[j].vt0
		}
		return vn.descBeg[i].val < vn.descBeg[j].val
	})
	vn.left = buildVT(lefts)
	vn.right = buildVT(rights)
	return vn
}

// stab 返回 (rt, vt) 处存活的半值集合。
func (idx *rectIndex) stab(rt, vt int64) map[int]bool {
	out := map[int]bool{}
	if idx == nil || len(idx.rtTicks) == 0 {
		return out
	}
	// 叶子 i 覆盖 [ticks[i], ticks[i+1])：取 ticks[i] <= rt 的最大 i。
	pos := sort.Search(len(idx.rtTicks)-1, func(i int) bool { return idx.rtTicks[i+1] > rt })
	if pos >= len(idx.rtTicks)-1 || idx.rtTicks[pos] > rt {
		return out
	}
	n := len(idx.tree) / 2
	node := n + pos
	win := map[int]winnerHit{}
	for node >= 1 {
		tree := idx.tree[node].tree
		if tree != nil {
			if idx.probe != nil {
				idx.probe.RuleScans++
			}
			stabVT(tree, vt, win, idx.probe)
		}
		node >>= 1
	}
	for code, h := range win {
		if h.alive {
			out[code] = true
		}
	}
	return out
}

type winnerHit struct {
	order int
	alive bool
}

func putWinner(out map[int]winnerHit, r rect) {
	if h, ok := out[r.val]; !ok || r.order >= h.order {
		out[r.val] = winnerHit{order: r.order, alive: r.alive}
	}
}

func stabVT(vn *vtNode, vt int64, out map[int]winnerHit, p *ProbeCounts) {
	for vn != nil {
		if p != nil {
			p.FactScans++
		}
		// 所有“覆盖 center”的区间均满足 vt0 <= center < vt1。
		// 若 vt >= center，则只需检查 vt1 > vt（ascEnd 按 vt1 升序）。
		// 若 vt < center，则只需检查 vt0 <= vt（descBeg 按 vt0 降序）。
		if vt >= vn.center {
			i := sort.Search(len(vn.ascEnd), func(i int) bool { return vn.ascEnd[i].vt1 > vt })
			for ; i < len(vn.ascEnd); i++ {
				putWinner(out, vn.ascEnd[i])
			}
			vn = vn.right
		} else {
			i := sort.Search(len(vn.descBeg), func(i int) bool { return vn.descBeg[i].vt0 <= vt })
			for ; i < len(vn.descBeg); i++ {
				putWinner(out, vn.descBeg[i])
			}
			vn = vn.left
		}
	}
}

// intervalIndex 是记录时间轴上的静态中心区间树。
type ivlNode struct {
	center  int64
	ascEnd  []ivl // 覆盖 center，按 hi 升序
	descBeg []ivl // 覆盖 center，按 lo 降序
	left    *ivlNode
	right   *ivlNode
}

type intervalIndex struct {
	root *ivlNode
}

func newIntervalIndex(is []ivl) *intervalIndex {
	cleaned := is[:0]
	for _, in := range is {
		if in.lo < in.hi {
			cleaned = append(cleaned, in)
		}
	}
	is = cleaned
	return &intervalIndex{root: buildIVL(is)}
}

func buildIVL(is []ivl) *ivlNode {
	if len(is) == 0 {
		return nil
	}
	bounds := make([]int64, 0, len(is)*2)
	for _, in := range is {
		bounds = append(bounds, in.lo)
		if in.hi != vtInf {
			bounds = append(bounds, in.hi-1)
		}
	}
	sort.Slice(bounds, func(i, j int) bool { return bounds[i] < bounds[j] })
	bounds = dedupInts(bounds)
	center := bounds[len(bounds)/2]

	var here, lefts, rights []ivl
	for _, in := range is {
		if in.lo <= center && center < in.hi {
			here = append(here, in)
		} else if in.hi <= center {
			lefts = append(lefts, in)
		} else {
			rights = append(rights, in)
		}
	}
	vn := &ivlNode{center: center}
	vn.ascEnd = append([]ivl{}, here...)
	sort.Slice(vn.ascEnd, func(i, j int) bool { return vn.ascEnd[i].hi < vn.ascEnd[j].hi })
	vn.descBeg = append([]ivl{}, here...)
	sort.Slice(vn.descBeg, func(i, j int) bool {
		if vn.descBeg[i].lo != vn.descBeg[j].lo {
			return vn.descBeg[i].lo > vn.descBeg[j].lo
		}
		return vn.descBeg[i].val < vn.descBeg[j].val
	})
	vn.left = buildIVL(lefts)
	vn.right = buildIVL(rights)
	return vn
}

func dedupInts(xs []int64) []int64 {
	if len(xs) == 0 {
		return xs
	}
	out := xs[:1]
	for i := 1; i < len(xs); i++ {
		if xs[i] != out[len(out)-1] {
			out = append(out, xs[i])
		}
	}
	return out
}

// stabPoint 返回覆盖点 t 的区间。
func (ix *intervalIndex) stabPoint(t int64) []ivl {
	var out []ivl
	vn := ix.root
	for vn != nil {
		if t >= vn.center {
			i := sort.Search(len(vn.ascEnd), func(i int) bool { return vn.ascEnd[i].hi > t })
			out = append(out, vn.ascEnd[i:]...)
			vn = vn.right
		} else {
			i := sort.Search(len(vn.descBeg), func(i int) bool { return vn.descBeg[i].lo <= t })
			out = append(out, vn.descBeg[i:]...)
			vn = vn.left
		}
	}
	return out
}

// overlapWindow 返回与半开窗口 [l, u) 相交的区间。
func (ix *intervalIndex) overlapWindow(l, u int64) []ivl {
	var out []ivl
	if ix == nil {
		return out
	}
	var walk func(vn *ivlNode)
	walk = func(vn *ivlNode) {
		if vn == nil {
			return
		}
		if u <= vn.center {
			// 只有 lo < u 且 hi > lo 的区间可能相交；左半继续递归。
			i := sort.Search(len(vn.descBeg), func(i int) bool { return vn.descBeg[i].lo < u })
			for ; i < len(vn.descBeg); i++ {
				if vn.descBeg[i].hi > l {
					out = append(out, vn.descBeg[i])
				}
			}
			walk(vn.left)
			return
		}
		if l > vn.center {
			i := sort.Search(len(vn.ascEnd), func(i int) bool { return vn.ascEnd[i].hi > l })
			for ; i < len(vn.ascEnd); i++ {
				if vn.ascEnd[i].lo < u {
					out = append(out, vn.ascEnd[i])
				}
			}
			walk(vn.right)
			return
		}
		// l <= center < u：覆盖 center 的全部区间只要与窗口相交即可。
		for _, in := range vn.ascEnd {
			if in.lo < u && in.hi > l {
				out = append(out, in)
			}
		}
		walk(vn.left)
		walk(vn.right)
	}
	walk(ix.root)
	return out
}
