// Package fire 计算两种模式的下一次触发时刻。
package fire

import "sync/atomic"

// checked 记录最近一次 rate 重排检查过的触发时刻个数（非导出，仅包内测试可读）。
var checked atomic.Int64

// Mode 调度模式。
type Mode int

const (
	Rate  Mode = iota // 固定频率：锚定绝对网格，跳过失触发
	Delay             // 固定延迟：上次结束 + interval
)

// NextRate 返回第一个 ≥ prevEnd 的 interval 整数倍时刻。
// 用一次除法直接定位，不逐个触发时刻循环，故 checked 恒为 1。
func NextRate(prevEnd, interval int64) int64 {
	checked.Store(1)
	return (prevEnd + interval - 1) / interval * interval
}

// NextDelay 返回上次结束 + interval。
func NextDelay(prevEnd, interval int64) int64 {
	return prevEnd + interval
}
