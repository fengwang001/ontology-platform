package fontcore

import "sort"

// seg 是一个覆盖登记项 id 的不相交码点段。
type seg struct {
	lo, hi rune
	id     int
}

// itNode 是中点区间树的一个节点：中心点 x；
// byLo 为覆盖 x 的段按左端点升序，byHi 为同样的段按右端点降序；
// 左右子树分别落在 x 两侧。
type itNode struct {
	x    rune
	byLo []seg
	byHi []seg
	lo   *itNode
	hi   *itNode
}

// rangeTree 是以中点为枢轴的区间树，用于按码点检索覆盖它的人脸。
// 查询复杂度 O(log R + k)，R 为不相交段数量，k 为命中数量；
// 不随区间（段）总数线性扫描。
type rangeTree struct {
	root *itNode
	segs int
	ids  []int // 局部 id -> 人脸 id（段级树使用；根树为空表示恒等）
}

func newRangeTree(ranges []RuneRange) *rangeTree {
	t := &rangeTree{}
	var segs []seg
	for id, rr := range ranges {
		segs = append(segs, seg{lo: rr.Lo, hi: rr.Hi, id: id})
	}
	t.root = buildIT(segs)
	t.segs = len(segs)
	return t
}

// query 返回覆盖 r 的所有登记项 id。
func (t *rangeTree) query(r rune, out []int) []int {
	n := t.root
	for n != nil {
		if r < n.x {
			for _, s := range n.byLo {
				if s.lo > r {
					break
				}
				if s.hi >= r {
					out = append(out, s.id)
				}
			}
			n = n.lo
		} else {
			for _, s := range n.byHi {
				if s.hi < r {
					break
				}
				if s.lo <= r {
					out = append(out, s.id)
				}
			}
			if r == n.x {
				return out
			}
			n = n.hi
		}
	}
	return out
}

// mergeRanges 把可能重叠/相邻的闭区间合并归一化为不相交、有序段。
func mergeRanges(in []RuneRange) []RuneRange {
	if len(in) == 0 {
		return nil
	}
	cp := make([]RuneRange, len(in))
	copy(cp, in)
	sort.Slice(cp, func(i, j int) bool {
		if cp[i].Lo != cp[j].Lo {
			return cp[i].Lo < cp[j].Lo
		}
		return cp[i].Hi < cp[j].Hi
	})
	out := cp[:1]
	for _, rr := range cp[1:] {
		last := &out[len(out)-1]
		if rr.Lo <= last.Hi+1 && rr.Hi+1 >= last.Lo {
			if rr.Hi > last.Hi {
				last.Hi = rr.Hi
			}
			if rr.Lo < last.Lo {
				last.Lo = rr.Lo
			}
			continue
		}
		out = append(out, rr)
	}
	return out
}

func buildIT(segs []seg) *itNode {
	if len(segs) == 0 {
		return nil
	}
	x := medianEndpoint(segs)
	n := &itNode{x: x}
	var left, right []seg
	for _, s := range segs {
		switch {
		case s.hi < x:
			left = append(left, s)
		case s.lo > x:
			right = append(right, s)
		default:
			n.byLo = append(n.byLo, s)
		}
	}
	sort.Slice(n.byLo, func(i, j int) bool {
		if n.byLo[i].lo != n.byLo[j].lo {
			return n.byLo[i].lo < n.byLo[j].lo
		}
		if n.byLo[i].hi != n.byLo[j].hi {
			return n.byLo[i].hi < n.byLo[j].hi
		}
		return n.byLo[i].id < n.byLo[j].id
	})
	n.byHi = make([]seg, len(n.byLo))
	copy(n.byHi, n.byLo)
	sort.SliceStable(n.byHi, func(i, j int) bool {
		if n.byHi[i].hi != n.byHi[j].hi {
			return n.byHi[i].hi > n.byHi[j].hi
		}
		return n.byHi[i].id < n.byHi[j].id
	})
	n.lo = buildIT(left)
	n.hi = buildIT(right)
	return n
}

// medianEndpoint 取所有端点的近似中位数作为枢轴，
// 保证递归深度 O(log R)。采用线性时间的中位端点近似：
// 收集每段中点并排序后取中值（构建期一次性开销）。
func medianEndpoint(segs []seg) rune {
	mids := make([]int, len(segs))
	for i, s := range segs {
		mids[i] = (int(s.lo) + int(s.hi)) / 2
	}
	sort.Ints(mids)
	return rune(mids[len(mids)/2])
}
