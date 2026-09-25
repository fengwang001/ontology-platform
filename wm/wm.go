// Package wm 跟踪单条流的水位线（迄今见过的最大事件 TS）。
package wm

import "math"

// Watermark 是单条流的水位线；未见任何事件时视为负无穷。
type Watermark struct {
	max  int64
	seen bool
}

// IsLate 报告 t 是否严格小于当前水位线（等于不算迟到）。
func (w *Watermark) IsLate(t int64) bool { return w.seen && t < w.max }

// Advance 记录 t；迟到则不改变状态并返回 false，否则推进水位线返回 true。
func (w *Watermark) Advance(t int64) bool {
	if w.IsLate(t) {
		return false
	}
	w.max = t
	w.seen = true
	return true
}

// Close 把水位线推到正无穷（收尾用）。
func (w *Watermark) Close() {
	w.max = math.MaxInt64
	w.seen = true
}

// Value 返回水位线；ok 为 false 表示该流尚未见任何事件。
func (w *Watermark) Value() (v int64, ok bool) { return w.max, w.seen }
