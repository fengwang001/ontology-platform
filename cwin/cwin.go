// Package cwin 只负责大窗口归属（含负时间戳向下取整）、子窗口序列、
// Emin、丢弃与触发判定，不依赖其他包。
package cwin

import "errors"

// ErrInvalidParams 是参数非法的哨兵错误：max/step 非正、max 不是 step
// 的整数倍、delay 为负。
var ErrInvalidParams = errors.New("cwin: invalid window parameters")

// Spec 是一组固定的窗口参数。
type Spec struct {
	max   int64
	step  int64
	delay int64
}

// New 校验参数并构造 Spec。
func New(max, step, delay int64) (Spec, error) {
	if max <= 0 || step <= 0 || max%step != 0 || delay < 0 {
		return Spec{}, ErrInvalidParams
	}
	return Spec{max: max, step: step, delay: delay}, nil
}

// Max 返回大窗口长度。
func (s Spec) Max() int64 { return s.max }

// Step 返回步长。
func (s Spec) Step() int64 { return s.step }

// Delay 返回水位线延迟。
func (s Spec) Delay() int64 { return s.delay }

// Steps 返回每个大窗口的子窗口个数 max/step。
func (s Spec) Steps() int { return int(s.max / s.step) }

// Start 返回 TS 所属大窗口的起点（向下取整，可为负）。
func (s Spec) Start(ts int64) int64 {
	q := ts / s.max
	if ts%s.max < 0 { // Go 整除向零截断，负余数时回退一格
		q--
	}
	return q * s.max
}

// EMin 返回大窗口起点 S 下，终点严格大于 TS 的第一个子窗口终点。
func (s Spec) EMin(S, ts int64) int64 {
	j := (ts-S)/s.step + 1 // ts-S 落在 [0,max)
	return S + j*s.step
}

// End 返回大窗口起点 S 的第 j 个子窗口终点（j 从 1 起）。
func (s Spec) End(S int64, j int) int64 { return S + int64(j)*s.step }
