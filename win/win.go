// Package win 只做窗口与水位线的纯计算，不依赖其他包。
// 窗口为左闭右开 [Start, End)，窗口下标 k 允许为负。
package win

import "math"

// 水位线端点哨兵：未见过任何事件时为负无穷；Flush 推进到正无穷。
const (
	NegInf = int64(math.MinInt64)
	PosInf = int64(math.MaxInt64)
)

// Window 是一个左闭右开的窗口。
type Window struct {
	Start int64
	End   int64
}

// Assign 计算 TS 所属窗口 [k*size, (k+1)*size)，size 必须为正。
// 起点用 ts 减去「向下取整余数」得到，避免 k*size 在 int64 端点溢出；
// 右端溢出时饱和到 MaxInt64（该窗口只可能在 Flush 的正无穷水位线下触发）。
func Assign(ts, size int64) Window {
	r := ts % size
	if r < 0 {
		r += size
	}
	start := ts - r
	end := start + size
	if end < start {
		end = math.MaxInt64
	}
	return Window{Start: start, End: end}
}

// Watermark 是只进不退的水位线；未见过事件前处于负无穷。
type Watermark struct {
	cur  int64
	seen bool
}

// Observe 用一个新见到的 TS 推进水位线（wm = maxTS - delay），返回推进后的值。
func (m *Watermark) Observe(ts, delay int64) int64 {
	w := ts - delay
	if !m.seen || w > m.cur {
		m.cur, m.seen = w, true
	}
	return m.cur
}

// Advance 把水位线单调地推进到给定值（Flush 用 PosInf）。
func (m *Watermark) Advance(wm int64) int64 {
	if !m.seen || wm > m.cur {
		m.cur, m.seen = wm, true
	}
	return m.cur
}

// Get 返回当前水位线；seen 为 false 表示尚为负无穷。
func (m *Watermark) Get() (wm int64, seen bool) { return m.cur, m.seen }

// Fires 为准点触发判定：wm >= End，同一窗口至多触发一次由调用方保证。
func Fires(wm int64, w Window) bool { return wm >= w.End }

// Late 为迟到判定：事件所属窗口已到准点触发线。
func Late(wm int64, w Window) bool { return wm >= w.End }

// addCapped 计算 a+b，溢出时饱和到 MaxInt64（b 必须非负）。
// Flush 把 wm 推到 MaxInt64，负窗口的 End+lateness 必须靠它才能正确比较。
func addCapped(a, b int64) int64 {
	if a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}

// AcceptLate 为「allowed-lateness 内」判定：wm < End+lateness（恰好等于不算）。
// 调用方需已确认事件迟到（wm >= End）。
func AcceptLate(wm int64, w Window, lateness int64) bool {
	return wm < addCapped(w.End, lateness)
}

// DropLate 为「allowed-lateness 之外」丢弃判定：wm >= End+lateness，边界归丢弃。
func DropLate(wm int64, w Window, lateness int64) bool {
	return wm >= addCapped(w.End, lateness)
}

// Purge 为窗口清除判定：wm >= End+lateness 时该窗口状态必须清除。
// 规则下与 DropLate 同阈值（清除的正是已不可再被迟到更新的窗口）。
func Purge(wm int64, w Window, lateness int64) bool {
	return wm >= addCapped(w.End, lateness)
}
