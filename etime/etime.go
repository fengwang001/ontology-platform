// Package etime 维护事件时间水位：maxEt、ETW 计算与迟到判定。
// 不依赖其他包；并发安全由上层（dual）负责。
package etime

// Watermark 是事件时间水位状态。零值不可用，须用 New 构造。
type Watermark struct {
	lateness int64
	maxEt    int64
	etw      int64
	seen     bool
	// scanCnt 记录最近一次 Ingest 为推进 maxEt/ETW、判定迟到而
	// 逐条检查过的事件个数。本实现只持有一个 maxEt 标量，恒为 0，
	// 用以证明 maxEt 是增量维护而非扫描已接受集合。
	scanCnt int
}

// New 构造水位。lateness 必须 >= 0（由上层校验，这里不再检查）。
func New(lateness int64) *Watermark {
	return &Watermark{lateness: lateness}
}

// Ingest 用本事件到达前的 ETW 判定迟到。
// 迟到（et <= ETW）：返回 false，状态不变。
// 按时：返回 true，并推进 maxEt 与 ETW = maxEt - lateness。
// 判定只比较标量，不扫描任何事件集合，scanCnt 恒为 0。
func (w *Watermark) Ingest(et int64) bool {
	w.scanCnt = 0
	if w.seen && et <= w.etw {
		return false
	}
	if !w.seen || et > w.maxEt {
		w.maxEt = et
		w.etw = et - w.lateness
	}
	w.seen = true
	return true
}

// ETW 返回当前事件时间水位；尚未见过任何事件时 ok=false（负无穷）。
func (w *Watermark) ETW() (v int64, ok bool) { return w.etw, w.seen }

// MaxEt 返回迄今最大事件时间；尚未见过任何事件时 ok=false。
func (w *Watermark) MaxEt() (v int64, ok bool) { return w.maxEt, w.seen }
