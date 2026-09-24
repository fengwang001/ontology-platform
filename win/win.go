// Package win 提供窗口归属计算（含负时间戳）与水位线推进/各类判定。
package win

// Window 是左闭右开区间 [Start, End)。
type Window struct{ Start, End int64 }

// Of 计算 ts 所属的窗口，窗口为 [k*size, (k+1)*size)，k 可为负。
func Of(ts, size int64) Window {
	k := ts / size
	if ts%size != 0 && ts < 0 {
		k--
	}
	return Window{k * size, (k + 1) * size}
}

// Watermark 只进不退；零值表示「负无穷」（尚无任何事件）。
type Watermark struct {
	v   int64
	set bool
}

// Advance 用事件时间 ts 推进水位线：wm = max(wm, ts-delay)。
func (w *Watermark) Advance(ts, delay int64) {
	if !w.set || ts-delay > w.v {
		w.v, w.set = ts-delay, true
	}
}

// Force 强制把水位线设为 v（用于 Flush 推进到正无穷）。
func (w *Watermark) Force(v int64) { w.v, w.set = v, true }

// Value 返回水位线；ok 为 false 表示负无穷。
func (w Watermark) Value() (v int64, ok bool) { return w.v, w.set }

// Geq 报告水位线是否 >= x；负无穷不小于任何值。
func (w Watermark) Geq(x int64) bool { return w.set && w.v >= x }
