// Package win 提供翻滚窗口的归属计算（含负时间戳）、触发判定、迟到判定与清除判定。
// 窗口为左闭右开区间 [k*size, (k+1)*size)，k 可为负。本包不依赖其他包。
package win

// Index 返回 ts 所属窗口的编号 k，对负时间戳按向下取整处理。
func Index(ts, size int64) int64 {
	k := ts / size
	if ts%size != 0 && ts < 0 {
		k--
	}
	return k
}

// Start 返回第 k 个窗口的左端点（含）。
func Start(k, size int64) int64 { return k * size }

// End 返回第 k 个窗口的右端点（不含）。
func End(k, size int64) int64 { return (k + 1) * size }

// Triggered 报告水位线 wm 是否触发结束时间为 end 的窗口（wm >= end）。
func Triggered(wm, end int64) bool { return wm >= end }

// Late 报告落入结束时间为 end 的窗口的事件，在水位线 wm 下是否迟到。
func Late(wm, end int64) bool { return wm >= end }

// Acceptable 报告迟到事件是否仍在允许迟到范围内：严格小于 wm < end+lateness。
func Acceptable(wm, end, lateness int64) bool { return wm < end+lateness }

// Expired 报告窗口状态是否必须清除：wm >= end+lateness（含边界）。
func Expired(wm, end, lateness int64) bool { return wm >= end+lateness }
