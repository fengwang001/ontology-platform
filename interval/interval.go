// Package interval 提供左闭右开区间 [Start, End) 与重叠判定。
package interval

import (
	"errors"
	"math"
)

// ErrEmpty 表示区间为空或倒置（Start >= End）。
var ErrEmpty = errors.New("interval: empty interval")

// Forever 表示「至今有效」的无穷远终点。
// 它是普通整数终点，与有限终点在比较上行为一致。
const Forever = math.MaxInt64

// Interval 是左闭右开区间 [Start, End)。
type Interval struct {
	Start int64
	End   int64
}

// New 构造区间，拒绝空区间与倒置区间。
func New(start, end int64) (Interval, error) {
	if start >= end {
		return Interval{}, ErrEmpty
	}
	return Interval{Start: start, End: end}, nil
}

// OK 报告区间是否非空。
func (i Interval) OK() bool { return i.Start < i.End }

// Contains 报告时刻 t 是否落在区间内：起点命中，终点不命中。
func (i Interval) Contains(t int64) bool {
	return i.Start <= t && t < i.End
}

// Overlaps 报告两个区间是否有公共时刻。
func (i Interval) Overlaps(o Interval) bool {
	return i.Start < o.End && o.Start < i.End
}
