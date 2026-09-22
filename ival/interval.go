package ival

import "fmt"

// Interval 是左闭右开区间 [L,R)，端点为 int64。
// L==R 表示零长度区间，其点集为空；L>R 非法。
type Interval struct {
	L int64
	R int64
}

// New 校验并构造区间。非法区间返回 ErrInvalidInterval，
// 该错误不改变任何调用方状态（这里只做纯值校验）。
func New(l, r int64) (Interval, error) {
	if l > r {
		return Interval{}, fmt.Errorf("ival: %w: [%d,%d)", ErrInvalidInterval, l, r)
	}
	return Interval{L: l, R: r}, nil
}

// Empty 报告零长度（点集为空）区间。
func (i Interval) Empty() bool { return i.L == i.R }

// Contains 报告点 x 是否属于 [L,R)。
func (i Interval) Contains(x int64) bool { return i.L <= x && x < i.R }

// Overlaps 报告两个区间的交点集是否非空。
// 交为 [max(L1,L2), min(R1,R2))，非空当且仅当 maxL < minR。
// 因此相接（共享一个端点）不重叠，零长度区间不与任何区间重叠。
func (i Interval) Overlaps(o Interval) bool {
	lo := i.L
	if o.L > lo {
		lo = o.L
	}
	hi := i.R
	if o.R < hi {
		hi = o.R
	}
	return lo < hi
}

// Abuts 报告两区间是否相接：交集为空且共享一个端点。
func (i Interval) Abuts(o Interval) bool {
	return i.R == o.L || o.R == i.L
}

// Intersection 返回交区间；不相交时 ok 为 false。
func (i Interval) Intersection(o Interval) (Interval, bool) {
	lo := i.L
	if o.L > lo {
		lo = o.L
	}
	hi := i.R
	if o.R < hi {
		hi = o.R
	}
	if lo >= hi {
		return Interval{}, false
	}
	return Interval{L: lo, R: hi}, true
}

// Union 返回并区间与是否存在“朴素并集”。
// 仅当两区间重叠或相接时并集是单个连续区间，此时 ok 为 true。
func (i Interval) Union(o Interval) (Interval, bool) {
	if !i.Overlaps(o) && !i.Abuts(o) {
		return Interval{}, false
	}
	lo := i.L
	if o.L < lo {
		lo = o.L
	}
	hi := i.R
	if o.R > hi {
		hi = o.R
	}
	return Interval{L: lo, R: hi}, true
}
