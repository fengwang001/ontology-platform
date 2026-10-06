package fontkernel

import "sort"

// centroidTree 是面向“点刺穿区间集合”查询的静态质心区间树。
//
// 每个树节点保存一个质心码点 c：
//   - 完全覆盖 c 的区间存入该节点（左、右各按端点排序），查询时对 r
//     只做一次二分前缀定位，不再进入它们所在的子树；
//   - 严格位于 c 左侧/右侧的区间递归进入左/右子树。
//
// 因此一次 stab 的节点访问次数为 O(log N)（每层最多沿一个孩子下降），
// 每次下降伴随常数次二分，判定某码点落入哪些区间的总开销为
// O(log N + k)，N 为区间总数、k 为命中区间数，均不随 N 线性增长。
// 树构造一次后只读，故并发查询无需额外加锁。
type centroidTree struct {
	root       *ctNode
	rangeCount int

	// StabCount 为自索引创建以来的区间比较次数，仅供可验证性能测试读取。
	StabCount int64
}

type ctNode struct {
	centroid rune
	left     []faceRuneRange // 覆盖 centroid，按 lo 升序（查询 r<c 时用）
	right    []faceRuneRange // 覆盖 centroid，按 hi 降序（查询 r>c 时用）
	loChild  *ctNode
	hiChild  *ctNode
}

// faceRuneRange 将归一化后的区间与其来源人脸上标绑定。
// 合并只发生在同一人脸内部，跨人脸的重叠区间必须各自保留。
type faceRuneRange struct {
	lo, hi rune
	face   int
}

func newCentroidTree(ranges []faceRuneRange) *centroidTree {
	t := &centroidTree{rangeCount: len(ranges)}
	t.root = t.build(cloneRanges(ranges))
	return t
}

func cloneRanges(in []faceRuneRange) []faceRuneRange {
	out := make([]faceRuneRange, len(in))
	copy(out, in)
	return out
}

func (t *centroidTree) build(rs []faceRuneRange) *ctNode {
	if len(rs) == 0 {
		return nil
	}
	// 质心取端点中位数，保证左右严格侧递归深度为 O(log N)。
	endpoints := make([]rune, 0, len(rs)*2)
	for _, r := range rs {
		endpoints = append(endpoints, r.lo, r.hi)
	}
	sort.Slice(endpoints, func(i, j int) bool { return endpoints[i] < endpoints[j] })
	c := endpoints[len(endpoints)/2]

	node := &ctNode{centroid: c}
	var loSide, hiSide []faceRuneRange
	for _, r := range rs {
		switch {
		case r.lo <= c && c <= r.hi:
			node.left = append(node.left, r)
			node.right = append(node.right, r)
		case r.hi < c:
			loSide = append(loSide, r)
		default: // r.lo > c
			hiSide = append(hiSide, r)
		}
	}
	sort.Slice(node.left, func(i, j int) bool {
		if node.left[i].lo != node.left[j].lo {
			return node.left[i].lo < node.left[j].lo
		}
		return node.left[i].face < node.left[j].face
	})
	sort.Slice(node.right, func(i, j int) bool {
		if node.right[i].hi != node.right[j].hi {
			return node.right[i].hi > node.right[j].hi
		}
		return node.right[i].face < node.right[j].face
	})
	node.loChild = t.build(loSide)
	node.hiChild = t.build(hiSide)
	return node
}

// stab 返回覆盖 r 的人脸上标（同一人脸可能因多个归一化区间出现多次，由调用方去重）。
func (t *centroidTree) stab(r rune) []int {
	var hits []int
	for n := t.root; n != nil; {
		if r == n.centroid {
			for _, rr := range n.left {
				t.StabCount++
				hits = append(hits, rr.face)
			}
			return hits
		}
		if r < n.centroid {
			// r<c：命中节点上覆盖 c 的区间当且仅当 lo<=r。left 按 lo 升序，取前缀。
			idx := sort.Search(len(n.left), func(i int) bool {
				t.StabCount++
				return n.left[i].lo > r
			})
			for i := 0; i < idx; i++ {
				hits = append(hits, n.left[i].face)
			}
			n = n.loChild
		} else {
			// r>c：命中当且仅当 hi>=r。right 按 hi 降序，取前缀。
			idx := sort.Search(len(n.right), func(i int) bool {
				t.StabCount++
				return n.right[i].hi < r
			})
			for i := 0; i < idx; i++ {
				hits = append(hits, n.right[i].face)
			}
			n = n.hiChild
		}
	}
	return hits
}

// Stats 暴露索引规模，供可验证性能测试使用。
type Stats struct {
	RangeCount int
}

func (t *centroidTree) stats() Stats { return Stats{RangeCount: t.rangeCount} }

// normalizeRanges 校验并归一化一张人脸的字符范围：逐区间排序后合并相交/相邻区间。
func normalizeRanges(in []RuneRange) ([]faceRuneRange, error) {
	if len(in) == 0 {
		return nil, ErrInvalidArgument
	}
	rs := make([]RuneRange, len(in))
	copy(rs, in)
	sort.Slice(rs, func(i, j int) bool {
		if rs[i].Lo != rs[j].Lo {
			return rs[i].Lo < rs[j].Lo
		}
		return rs[i].Hi < rs[j].Hi
	})
	var out []faceRuneRange
	for _, r := range rs {
		if r.Lo > r.Hi || r.Lo < 0 {
			return nil, ErrInvalidArgument
		}
		if n := len(out); n > 0 && r.Lo <= out[n-1].hi+1 {
			if r.Hi > out[n-1].hi {
				out[n-1].hi = r.Hi
			}
			continue
		}
		out = append(out, faceRuneRange{lo: r.Lo, hi: r.Hi})
	}
	return out, nil
}
