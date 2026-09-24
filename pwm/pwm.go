// Package pwm 维护单个输入分区的状态：分区水位与最后活跃时间。
// 它不依赖其他任何包，也不感知时钟——时间由上层传入。
package pwm

import "math"

// NegInf 是分区从未上报时的水位（负无穷）。
const NegInf = math.MinInt64

// Part 是单个分区的全部状态。
type Part struct {
	water int64 // 分区水位，只进不退
	last  int64 // 最后活跃时间
}

// New 创建一个水位为负无穷、最后活跃时间为 t0 的分区。
func New(t0 int64) *Part {
	return &Part{water: NegInf, last: t0}
}

// Water 返回当前分区水位。
func (p *Part) Water() int64 { return p.water }

// Last 返回最后活跃时间（最近一次成功 Report 的 now，初始为 t0）。
func (p *Part) Last() int64 { return p.last }

// CanReport 判断 w 能否被接受：w 不得小于当前水位，相等允许。
func (p *Part) CanReport(w int64) bool { return w >= p.water }

// Report 接受一次上报：水位置为 w、最后活跃时间置为 now。
// 调用方必须先通过 CanReport 与其他全部校验，本函数不返回错误、
// 也不允许在被拒绝的路径上被调用，从而保证失败不留痕。
func (p *Part) Report(w, now int64) {
	p.water = w
	p.last = now
}

// Idle 判定在处理时刻 now 本分区是否空闲：now-last >= idle 即为空闲，
// 恰好相等也算空闲；idle 必须为正（由上层构造时保证）。
//
// 比较写成「now 是否达到阈值 last+idle」而非直接算 now-last，
// 避免跨度接近 int64 范围时差值溢出翻转：若 last+idle 本身溢出，
// 阈值超过任何可能的 now，必为非空闲。
func (p *Part) Idle(now, idle int64) bool {
	if p.last > math.MaxInt64-idle {
		return false
	}
	return now >= p.last+idle
}
