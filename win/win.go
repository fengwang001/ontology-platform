// Package win 判定事件时间戳是否落在滑动去重窗口内。
// 窗口是半开区间 [last, last+W)：d == W 恰在边界，算窗口外（接受）。
package win

// Duplicate 报告时间戳 ts 相对最近一次被接受的 last 是否重复。
// ts <= last 是更旧或相等的乱序事件，一律重复；
// ts > last 且 ts-last < W（严格小于）落在窗口内，重复；
// ts-last >= W 在窗口外，不重复（应接受）。
// 前置条件：w > 0（由调用方保证）。
func Duplicate(last, ts, w int64) bool {
	return ts <= last || ts-last < w
}
