// Package wm 维护单个 Key 的事件时间水位，并对事件做主路/侧路判定。
//
// 它不依赖其他包，也不保存任何历史事件：判定只与一个标量水位比较，
// 因此单次判定为 O(1)。
package wm

// Watermark 是单个 Key 的单调不减水位。零值表示该 Key 尚未出现。
type Watermark struct {
	v      int64 // 当前水位（仅 set 后有意义）
	set    bool  // 该 Key 是否已经出现过
	checks int   // 最近一次判定所检查的历史事件个数；非导出，标量实现下恒为 0
}

// Seen 报告该 Key 是否已经出现过。
func (w *Watermark) Seen() bool { return w.set }

// Value 返回当前水位；该 Key 未出现时 ok 为 false。
func (w *Watermark) Value() (v int64, ok bool) { return w.v, w.set }

// Classify 按当前水位判定事件时间 ts 属于主路还是侧路：
//   - 该 Key 首次出现：恒为主路；
//   - ts >= 水位：主路（相等也算主路），gap 为 0；
//   - ts <  水位：侧路，gap = 水位 - ts（必 > 0）。
//
// 判定本身不改变水位。它只比较存储的标量水位，不扫描任何历史事件，
// 故每次检查的历史事件个数为 0（O(1)）。
func (w *Watermark) Classify(ts int64) (mainRoad bool, gap int64) {
	w.checks = 0 // 只与存储的标量水位比较，检查的历史事件数恒为 0
	if !w.set || ts >= w.v {
		return true, 0
	}
	return false, w.v - ts
}

// Advance 用一条主路事件的 ts 推进水位：仅在首见或 ts 严格更大时写入，
// 因此水位单调不减，相等与迟到都不会改动它。返回水位是否发生变化。
func (w *Watermark) Advance(ts int64) bool {
	if !w.set || ts > w.v {
		w.v, w.set = ts, true
		return true
	}
	return false
}
