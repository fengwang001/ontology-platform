// Package tm 提供水位线推进、可发射判定与 outTS 上钳。不依赖其他包。
package tm

// Watermark 跟踪迄今见过的最大事件时间，wm = maxSeen - delay，只进不退。
// 零值不可用，须用 New 构造。
type Watermark struct {
	delay   int64
	maxSeen int64
	seen    bool
	flushed bool // Flush 后视为 +无穷
}

// New 构造水位线，delay 必须 >= 0（调用方负责校验）。
func New(delay int64) *Watermark { return &Watermark{delay: delay} }

// Observe 用 ts 推进 maxSeen；wm 随之只增不减。
func (w *Watermark) Observe(ts int64) {
	if !w.seen || ts > w.maxSeen {
		w.maxSeen = ts
		w.seen = true
	}
}

// Value 返回当前水位线；ok=false 表示「负无穷」（尚无事件到达）。
func (w *Watermark) Value() (wm int64, ok bool) {
	if !w.seen {
		return 0, false
	}
	return w.maxSeen - w.delay, true
}

// Emittable 判定 TS <= wm（含相等）；Flush 后一切可发射。
func (w *Watermark) Emittable(ts int64) bool {
	if w.flushed {
		return true
	}
	wm, ok := w.Value()
	return ok && ts <= wm
}

// Flush 把水位线置为 +无穷。
func (w *Watermark) Flush() { w.flushed = true }

// Clamp 返回 max(ts, last)，用于 outTS 上钳。
func Clamp(ts, last int64) int64 {
	if ts > last {
		return ts
	}
	return last
}
