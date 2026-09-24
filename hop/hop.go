// Package hop 计算跳跃窗口的归属：给定事件时间 TS，求出所有包含它的
// 左闭右开窗口 [k*slide, k*slide+size)。负时间戳一律按数学意义向下取整。
package hop

// floorDiv 返回 a/b 向下取整（b 必须为正）。
func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && a < 0 {
		q--
	}
	return q
}

// Starts 返回包含 ts 的全部窗口的起点，升序，恰好 size/slide 个。
// 要求 size>0、slide>0 且 size%slide==0（由调用方保证）。
func Starts(ts, size, slide int64) []int64 {
	n := size / slide
	kmax := floorDiv(ts, slide) // ts 所在滑动格：kmax*slide <= ts < (kmax+1)*slide
	out := make([]int64, n)
	for i := range out {
		out[i] = (kmax - n + 1 + int64(i)) * slide
	}
	return out
}

// Contains 报告 ts 是否落在窗口 [start, start+size) 内。
func Contains(ts, start, size int64) bool {
	return start <= ts && ts < start+size
}
