// Package buf 实现按水位线缓冲、发射阶段、Flush 与输出序列维护。依赖 tm。
package buf

import (
	"sort"

	"ontology/tm"
)

// Event 是上游到达的事件。
type Event struct {
	TS int64
	ID string
}

// Out 是已发射的输出项，OutTS >= TS。
type Out struct {
	ID    string
	TS    int64
	OutTS int64
}

// Buffer 非并发安全，并发保护由调用方（api 包）负责。
type Buffer struct {
	wm      *tm.Watermark
	pending []Event // 按 (TS, 到达序) 升序；同 TS 先到者在前
	out     []Out
	last    int64 // 上一个已发射的 outTS
	hasLast bool
	reads   int // 最近一次发射阶段中读取比较的候选事件个数（非导出，不进公开接口）
}

// New 构造缓冲区，wm 的生命周期与 Buffer 相同。
func New(wm *tm.Watermark) *Buffer { return &Buffer{wm: wm} }

// Pending 返回当前缓冲事件数。
func (b *Buffer) Pending() int { return len(b.pending) }

// WM 返回当前水位线（ok=false 表示负无穷）。
func (b *Buffer) WM() (int64, bool) { return b.wm.Value() }

// Feed 处理一批到达事件：逐条推进水位线并按 (TS, 到达序) 入缓冲，
// 全部落地后统一执行一次发射阶段，返回本批发射项。
func (b *Buffer) Feed(evs []Event) []Out {
	for _, e := range evs {
		b.wm.Observe(e.TS)
		i := sort.Search(len(b.pending), func(i int) bool { return b.pending[i].TS > e.TS })
		b.pending = append(b.pending, Event{})
		copy(b.pending[i+1:], b.pending[i:])
		b.pending[i] = e
	}
	return b.Emit()
}

// Emit 执行一次发射阶段：反复取缓冲中最小者，若 TS <= wm 则上钳发射。
func (b *Buffer) Emit() []Out {
	b.reads = 0
	var emitted []Out
	for len(b.pending) > 0 {
		b.reads++
		e := b.pending[0]
		if !b.wm.Emittable(e.TS) {
			break
		}
		out := Out{ID: e.ID, TS: e.TS, OutTS: e.TS}
		if b.hasLast {
			out.OutTS = tm.Clamp(e.TS, b.last)
		}
		b.last, b.hasLast = out.OutTS, true
		b.out = append(b.out, out)
		emitted = append(emitted, out)
		b.pending = b.pending[1:]
	}
	return emitted
}

// Flush 把水位线置为 +无穷 后再执行一次发射阶段。
func (b *Buffer) Flush() []Out {
	b.wm.Flush()
	return b.Emit()
}

// Output 返回已发射序列的副本。
func (b *Buffer) Output() []Out {
	out := make([]Out, len(b.out))
	copy(out, b.out)
	return out
}
