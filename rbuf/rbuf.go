// Package rbuf 维护按 (TS, Seq) 取最小的事件缓冲，按水位线释放主输出，
// 并累积旁路输出。它只懂有序缓冲与产出，不做迟到判定与去重（那些在 api 层）。
package rbuf

import (
	"container/heap"
	"ontology/order"
)

// Event 是缓冲与输出中的一条事件。
type Event struct {
	ID  string
	TS  int64
	Seq int64
}

func (e Event) key() order.Key { return order.Key{TS: e.TS, Seq: e.Seq} }

// Buffer 是有界最小堆缓冲；并发安全由上层 api 加锁保证。
type Buffer struct {
	h minHeap
	// main 是迄今全部主输出，side 是迄今全部旁路输出（旁路保持到达顺序）。
	main []Event
	side []Event
	// lastProbes 记录最近一次 Release 检查过的缓冲事件个数（非导出复杂度探针，
	// 仅供本包内部测试读取；不存在任何导出访问路径）。
	lastProbes int
}

// Len 返回当前缓冲事件数（超限判定须在事件入缓冲之前调用）。
func (b *Buffer) Len() int { return b.h.Len() }

// Buffer 把一个已接受的非迟到事件放入堆中。
func (b *Buffer) Buffer(e Event) { heap.Push(&b.h, e) }

// Bypass 把一个迟到事件按到达顺序追加到旁路输出。
func (b *Buffer) Bypass(e Event) { b.side = append(b.side, e) }

// Release 弹出缓冲中全部 TS <= wm 的事件，按 (TS, Seq) 升序追加进主输出，
// 并返回本次新增的主输出。由于堆顶恒为最小键，只需逐个检查堆顶：
// 弹出 k 个后再看一个未弹出者即止（缓冲恰好排空时无此次额外检查），
// 故检查次数为 k 或 k+1，与缓冲总量 m 无关。
func (b *Buffer) Release(wm int64) []Event {
	b.lastProbes = 0
	var add []Event
	for b.h.Len() > 0 {
		b.lastProbes++
		top := b.h[0]
		if top.TS > wm {
			break
		}
		add = append(add, heap.Pop(&b.h).(Event))
	}
	b.main = append(b.main, add...)
	return add
}

// Flush 以正无穷水位线释放全部缓冲，返回本次新增的主输出。
func (b *Buffer) Flush() []Event { return b.Release(order.PosInf) }

// Main 返回迄今全部主输出的副本。
func (b *Buffer) Main() []Event { return append([]Event(nil), b.main...) }

// Side 返回迄今全部旁路输出的副本（保持到达顺序）。
func (b *Buffer) Side() []Event { return append([]Event(nil), b.side...) }

// minHeap 以 order.Less 定序：TS 升序，同 TS 时 Seq 升序（先到先出）。
type minHeap []Event

func (h minHeap) Len() int { return len(h) }
func (h minHeap) Less(i, j int) bool {
	return order.Less(h[i].key(), h[j].key())
}
func (h minHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *minHeap) Push(x any)   { *h = append(*h, x.(Event)) }
func (h *minHeap) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	*h = old[:n-1]
	return e
}
