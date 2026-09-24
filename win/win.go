// Package win 是无依赖的窗口语义层：窗口归属（含负时间戳）、
// 水位线推进、early/on-time/late 三档触发判定与清除判定。
package win

// Window 是左闭右开区间 [Start, End)。
type Window struct {
	Start int64
	End   int64
}

// Assign 返回时间戳 ts 在大小为 size 的翻滚窗口中的归属。
// k 可以为负：整除按 floor（向负无穷）而不是向零截断。
func Assign(ts, size int64) Window {
	k := ts / size
	if ts%size < 0 { // Go 的余数与被除数同号：余数为负说明整除被向零截断
		k--
	}
	start := k * size
	return Window{Start: start, End: start + size}
}

// Bump 在观测到事件时间 ts 后更新「迄今最大 TS」。
// seen 表示此前是否已见过事件；未见时以 ts 自身为初值。
func Bump(maxTS int64, seen bool, ts int64) (int64, bool) {
	if !seen || ts > maxTS {
		return ts, true
	}
	return maxTS, true
}

// Watermark 由最大事件时间计算水位线 maxTS-delay；调用方负责只进不退。
func Watermark(maxTS, delay int64) int64 { return maxTS - delay }

// EarlyDue 判断计数从 prev 增到 count 时是否跨过 early 的下一个正整数倍。
func EarlyDue(prev, count, early int64) bool { return count/early > prev/early }

// CanEarly 报告水位线未越过 end 时是否允许早触发（水位线无效视为负无穷）。
func CanEarly(wm int64, valid bool, end int64) bool {
	return !valid || wm < end
}

// OnTimeDue 报告是否到达/越过窗口结束时间（应触发 on-time）。
func OnTimeDue(wm int64, valid bool, end int64) bool {
	return valid && wm >= end
}

// LateOpen 报告 on-time 之后窗口是否仍处于 lateness 宽限区内。
func LateOpen(wm int64, valid bool, end, lateness int64) bool {
	return valid && wm >= end && wm < end+lateness
}

// PurgeDue 报告是否应清除窗口状态（wm >= end+lateness）。
func PurgeDue(wm int64, valid bool, end, lateness int64) bool {
	return valid && wm >= end+lateness
}
