// Package wm 只负责事件时间水位线的值与推进规则：
// 水位推进、数据/心跳的贡献计算、迟到心跳判定。
// 本包不依赖工程内任何其他包。
package wm

import "math"

// NegInfinity 是水位的初始值「负无穷」。
// 事件 TS 规定为非负 int64，故用最小 int64 表示负无穷，
// 它不会与任何合法贡献相等，也保证首批事件一定能把水位抬起。
const NegInfinity = int64(math.MinInt64)

// Watermark 是单个水位线。零值不可用，请用 New 构造。
type Watermark struct {
	v int64
}

// New 返回初始值为负无穷的水位线。
func New() Watermark {
	return Watermark{v: NegInfinity}
}

// Value 返回当前水位值。
func (w Watermark) Value() int64 {
	return w.v
}

// Advance 按 wm = max(wm, contribution) 推进水位并返回推进后的值。
// 只进不退：contribution 不大于当前值时水位原样保留。
func (w *Watermark) Advance(contribution int64) int64 {
	if contribution > w.v {
		w.v = contribution
	}
	return w.v
}

// DataContribution 是数据事件 {Key, TS} 对水位的贡献：TS-delay。
// 数据事件要扣掉乱序容忍延迟 delay。
func DataContribution(ts, delay int64) int64 {
	return ts - delay
}

// HeartbeatContribution 是心跳事件 {TS} 对水位的贡献：TS 本身。
// 心跳是上游「不会再有更早事件」的承诺，TS 直接就是水位下界，不扣 delay。
func HeartbeatContribution(ts int64) int64 {
	return ts
}

// IsLateHeartbeat 判定一个心跳是否迟到：其 TS 不大于当前水位即为迟到。
// 判定只比较 TS 与当前水位两个标量，不查看任何历史事件。
func IsLateHeartbeat(ts, current int64) bool {
	return ts <= current
}
