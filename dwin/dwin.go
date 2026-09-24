// Package dwin 负责事件时间水位线的单调推进与过期边界判定。
// 本包不依赖其他包。
package dwin

import "math"

// Watermark 是事件时间水位线：wm = 迄今最大 TS − delay，只进不退。
// seen 为 false（一个事件都没见过）时水位线为负无穷。
type Watermark struct {
	delay int64
	maxTS int64
	seen  bool
}

// New 创建延迟为 delay 的水位线。
func New(delay int64) *Watermark {
	return &Watermark{delay: delay}
}

// Observe 纳入一个事件 TS：更新最大 TS（水位线随之只进不退），
// 返回新水位线；valid 为 false 表示仍为负无穷（调用方不会在无事件时调用）；
// advanced 报告本次是否真的推进了水位线。
func (w *Watermark) Observe(ts int64) (wm int64, valid, advanced bool) {
	advanced = !w.seen || ts > w.maxTS
	if advanced {
		w.maxTS = ts
	}
	w.seen = true
	wm, valid = w.Get()
	return
}

// Get 返回当前水位线；valid 为 false 表示负无穷。
func (w *Watermark) Get() (wm int64, valid bool) {
	if !w.seen {
		return math.MinInt64, false
	}
	return w.maxTS - w.delay, true
}

// Restore 恢复水位线的内部状态，仅供上层在整批失败时回滚，正常处理路径不得调用。
func (w *Watermark) Restore(maxTS int64, seen bool) {
	w.maxTS = maxTS
	w.seen = seen
}

// Snapshot 返回供 Restore 使用的内部状态。
func (w *Watermark) Snapshot() (maxTS int64, seen bool) {
	return w.maxTS, w.seen
}

// Expired 判定首见 TS 为 firstTS 的记忆是否已过期：wm >= firstTS + ttl（边界含等号）。
// 水位线为负无穷时永不过期。
func Expired(wm int64, valid bool, firstTS, ttl int64) bool {
	if !valid {
		return false
	}
	return wm >= firstTS+ttl
}
