// Package win 计算跳变（滑动）窗口归属与触发判定，不依赖其他包。
package win

// Window 是左闭右开区间 [Start, End)。
type Window struct {
	Start, End int64
}

// floorDiv 是向下取整除法（b>0），负时间戳也能正确定位 n*hop。
func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && a < 0 {
		q--
	}
	return q
}

// Windows 返回事件时间 ts 落入的恰好 size/hop 个重叠窗口，按 Start 升序。
// 要求 size 为 hop 的正整数倍（由 wagg.New 保证）。
func Windows(ts, size, hop int64) []Window {
	k := size / hop
	last := floorDiv(ts, hop) * hop // ts 所在 hop 格子的左端，即最晚的窗口起点
	ws := make([]Window, 0, k)
	for s := last - (k-1)*hop; s <= last; s += hop {
		ws = append(ws, Window{Start: s, End: s + size})
	}
	return ws
}

// Fired 判定水位线 wm 是否触发右端为 end 的窗口（wm >= end）。
func Fired(end, wm int64) bool {
	return wm >= end
}
