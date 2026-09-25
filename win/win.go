// Package win 判定事件相对滑动窗口的归属：重复还是接受。
// 窗口为半开区间 [last, last+W)：与最近一次被接受事件的 TS 差
// 严格小于 W 算重复，大于等于 W 算接受；TS <= last 的乱序事件一律重复。
package win

// Duplicate 报告时间戳为 ts 的事件是否重复。
// last 为同一 ID 最近一次被接受事件的 TS，w 为窗口宽度（w > 0）。
func Duplicate(last, ts, w int64) bool {
	if ts <= last { // 更旧或相等的乱序事件
		return true
	}
	return ts-last < w // 半开边界：d == w 落在窗外，判接受
}
