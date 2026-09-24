// Package interval 提供左闭右开区间 [Start, End) 及其重叠判定。
package interval

import (
	"errors"
	"math"
)

// Infinity 表示「至今有效」的无穷远终点，是普通 int64，
// 与有限终点共用同一套比较，无需特判。
const Infinity = math.MaxInt64

// ErrEmpty 表示空区间（起点大于等于终点）。
var ErrEmpty = errors.New("interval: empty interval")

// I 是左闭右开区间 [Start, End)。
type I struct {
	Start int64
	End   int64
}

// New 构造区间，拒绝空区间（Start >= End）。
func New(start, end int64) (I, error) {
	if start >= end {
		return I{}, ErrEmpty
	}
	return I{Start: start, End: end}, nil
}

// Empty 判定区间是否为空。
func (i I) Empty() bool { return i.Start >= i.End }

// Contains 判定时刻 t 是否落在区间内：起点命中，终点不命中。
func (i I) Contains(t int64) bool { return i.Start <= t && t < i.End }

// Overlaps 判定两个区间是否有公共点。
func (i I) Overlaps(o I) bool { return i.Start < o.End && o.Start < i.End }
