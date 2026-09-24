// Package win 只做窗口归属与纯判定：窗口对齐（含负时间戳）、触发、迟到、清除。
// 本包不依赖其他包。所有判定都是纯函数，水位线用 (has, value) 表达负无穷。
package win

import "math"

// Window 是左闭右开区间 [Start, End)。
type Window struct {
	Start int64
	End   int64
}

// Of 返回 ts 在大小为 size 的翻滚窗口中的归属。size 必须为正。
// k = floor(ts/size)，Go 的整除向零取整，负余数需向下修正一格。
func Of(ts, size int64) Window {
	k := ts / size
	if ts < 0 && ts%size != 0 {
		k--
	}
	start := k * size
	return Window{Start: start, End: start + size}
}

// ShouldFire 在 wm >= end 时为真：窗口应触发并输出当前计数。
// 水位线为负无穷（has==false）时永不触发。
func ShouldFire(end int64, hasWM bool, wm int64) bool {
	return hasWM && wm >= end
}

// IsLate 与触发同界：wm >= end 后到达该窗口的事件即为迟到。
func IsLate(end int64, hasWM bool, wm int64) bool {
	return hasWM && wm >= end
}

// AcceptLate 在严格 wm < end+lateness 时为真；等于上界即拒绝（边界语义）。
func AcceptLate(end, lateness int64, hasWM bool, wm int64) bool {
	return !hasWM || wm < end+lateness
}

// ShouldPurge 在 wm >= end+lateness 时为真：窗口状态必须清除，不占内存。
func ShouldPurge(end, lateness int64, hasWM bool, wm int64) bool {
	return hasWM && wm >= end+lateness
}

// SatAdd 饱和加减：极端时间戳（含把水位线推到正无穷的 MaxInt64）加减时不溢出翻转。
func SatAdd(x, y int64) int64 {
	s := x + y
	if y > 0 && s < x {
		return math.MaxInt64
	}
	if y < 0 && s > x {
		return math.MinInt64
	}
	return s
}
