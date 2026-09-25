// Package win 定义滑动窗口的区间语义：左开右闭 (wm-size, wm]。
// 它不依赖其他任何包。
package win

// Bound 返回窗口左边界 wm-size。窗口区间为 (Bound, wm]，左开右闭。
func Bound(wm, size int64) int64 { return wm - size }

// InWindow 判定事件时间 ts 是否落在窗口 (wm-size, wm] 内。
// 左边界本身不在窗口内（左开）。
func InWindow(ts, wm, size int64) bool { return ts > wm-size && ts <= wm }

// Expired 判定时间 ts 的事件在水位线推进到 wm 后是否过期：
// 所有 ts <= wm-size 的事件都应从窗口移除（左边界恰好过期）。
func Expired(ts, wm, size int64) bool { return ts <= wm-size }
