// Package win 提供翻滚窗口的归属计算与水位线判定，不依赖其他包。
package win

// Index 返回 ts 所属窗口的下标 k，窗口为 [k*size, (k+1)*size)，k 可为负。
// Go 的 / 向零取整，负时间戳需要向下（floor）修正。
func Index(ts, size int64) int64 {
	i := ts / size
	if ts%size != 0 && ts < 0 {
		i--
	}
	return i
}

// Bounds 返回 ts 所属窗口的 [start, end)，左闭右开。
func Bounds(ts, size int64) (start, end int64) {
	start = Index(ts, size) * size
	return start, start + size
}

// Watermark 由迄今最大事件时间计算水位线。
func Watermark(maxTS, delay int64) int64 { return maxTS - delay }

// Triggered 判定窗口是否触发：wm >= end。
func Triggered(wm, end int64) bool { return wm >= end }

// Acceptable 判定迟到事件是否仍可接受：wm < end+lateness。
func Acceptable(wm, end, lateness int64) bool { return wm < end+lateness }

// Purgeable 判定窗口状态是否必须清除：wm >= end+lateness。
func Purgeable(wm, end, lateness int64) bool { return wm >= end+lateness }
