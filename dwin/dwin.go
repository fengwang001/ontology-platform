// Package dwin 维护事件时间水位线并提供过期判定。
// 水位线 wm = 迄今见过的最大事件 TS − delay，只进不退；
// 未见过任何事件时水位线为负无穷。
package dwin

import (
	"math"
)

// NegInf 表示负无穷水位线。
const NegInf = math.MinInt64

// Watermark 是单调水位线。零值不可用，须用 New 构造。
type Watermark struct {
	delay int64
	maxTS int64
	seen  bool
}

// New 以延迟 delay 构造水位线。
func New(delay int64) *Watermark { return &Watermark{delay: delay} }

// Observe 用事件 TS 推进水位线（重复与新事件规则相同），返回推进后的 wm。
func (w *Watermark) Observe(ts int64) int64 {
	if !w.seen || ts > w.maxTS {
		w.maxTS = ts
		w.seen = true
	}
	wm, _ := w.Value()
	return wm
}

// Value 返回当前水位线；seen 为 false 时返回 (NegInf, false)。
func (w *Watermark) Value() (wm int64, seen bool) {
	if !w.seen {
		return NegInf, false
	}
	return w.maxTS - w.delay, true
}

// Snapshot 返回水位线原始状态，供上层整批失败时回滚。
func (w *Watermark) Snapshot() (maxTS int64, seen bool) {
	return w.maxTS, w.seen
}

// Restore 恢复水位线状态。
func (w *Watermark) Restore(maxTS int64, seen bool) {
	w.maxTS, w.seen = maxTS, seen
}

// Expired 判定首见 TS 为 firstTS 的记忆在水位线 wm 下是否过期。
// 边界规则：wm >= firstTS + ttl 即过期（等号算过期）。
// wm 为负无穷时永不过期。
func Expired(wm, firstTS, ttl int64) bool {
	if wm == NegInf {
		return false
	}
	// 调用方保证 ttl>0，故只有 firstTS>0 时 firstTS+ttl 可能溢出上界；
	// 过期点超出 int64 上界则有限 wm 永不到达。
	if firstTS > 0 && firstTS > math.MaxInt64-ttl {
		return false
	}
	return wm >= firstTS+ttl
}
